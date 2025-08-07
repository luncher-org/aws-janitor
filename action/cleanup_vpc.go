package action

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

func (a *action) cleanVPCs(ctx context.Context, input *CleanupScope) error {
	client := ec2.New(input.Session)

	vpcsToDelete := []*ec2.Vpc{}
	pageFunc := func(page *ec2.DescribeVpcsOutput, _ bool) bool {
		for _, vpc := range page.Vpcs {
			var ignore, markedForDeletion, managedByCloudFormation bool
			for _, tag := range vpc.Tags {
				switch *tag.Key {
				case input.IgnoreTag:
					ignore = true
				case DeletionTag:
					markedForDeletion = true
				case "aws:cloudformation:stack-name", "aws:cloudformation:stack-id":
					managedByCloudFormation = true
				}
			}

			if ignore || aws.BoolValue(vpc.IsDefault) {
				LogDebug("vpc %s has ignore tag or is a default vpc, skipping cleanup", *vpc.VpcId)
				continue
			}

			if managedByCloudFormation {
				LogDebug("vpc %s is managed by CloudFormation, should be cleaned by stack deletion, skipping", *vpc.VpcId)
				continue
			}

			if !markedForDeletion {
				// NOTE: only mark for future deletion if we're not running in dry-mode
				if a.commit {
					LogDebug("vpc %s does not have deletion tag, marking for future deletion and skipping cleanup", *vpc.VpcId)
					if err := a.markVPCForFutureDeletion(ctx, *vpc.VpcId, client); err != nil {
						LogError("failed to mark vpc %s for future deletion: %s", *vpc.VpcId, err.Error())
					}
				}
				continue
			}

			LogDebug("adding vpc %s to delete list", *vpc.VpcId)
			vpcsToDelete = append(vpcsToDelete, vpc)
		}

		return true
	}

	if err := client.DescribeVpcsPagesWithContext(ctx, &ec2.DescribeVpcsInput{}, pageFunc); err != nil {
		return fmt.Errorf("failed getting list of vpcs: %w", err)
	}

	if len(vpcsToDelete) == 0 {
		Log("no vpcs to delete")
		return nil
	}

	for _, vpc := range vpcsToDelete {
		if !a.commit {
			LogDebug("skipping deletion of vpc %s as running in dry-mode", *vpc.VpcId)
			continue
		}

		if err := a.deleteVPC(ctx, *vpc.VpcId, client); err != nil {
			LogError("failed to delete vpc %s: %s", *vpc.VpcId, err.Error())
		}
	}

	return nil
}

func (a *action) markVPCForFutureDeletion(ctx context.Context, vpcId string, client *ec2.EC2) error {
	Log("Marking VPC %s for future deletion", vpcId)

	_, err := client.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
		Resources: []*string{&vpcId}, Tags: []*ec2.Tag{
			{Key: aws.String(DeletionTag), Value: aws.String("true")},
		},
	})

	return err
}

func (a *action) deleteVPC(ctx context.Context, vpcId string, client *ec2.EC2) error {
	Log("Deleting VPC %s and its dependencies", vpcId)

	if err := a.cleanVPCDependencies(ctx, vpcId, client); err != nil {
		LogError("failed to clean VPC dependencies for %s: %s", vpcId, err.Error())
	}

	if _, err := client.DeleteVpcWithContext(ctx, &ec2.DeleteVpcInput{VpcId: &vpcId}); err != nil {
		return fmt.Errorf("failed to delete vpc %s: %w", vpcId, err)
	}

	Log("Successfully deleted VPC %s", vpcId)
	return nil
}

func (a *action) cleanVPCDependencies(ctx context.Context, vpcId string, client *ec2.EC2) error {
	LogDebug("Cleaning VPC dependencies for %s", vpcId)

	if err := a.deleteNATGateways(ctx, vpcId, client); err != nil {
		LogError("failed to delete NAT gateways for VPC %s: %s", vpcId, err.Error())
	}

	if err := a.deleteInternetGateways(ctx, vpcId, client); err != nil {
		LogError("failed to delete internet gateways for VPC %s: %s", vpcId, err.Error())
	}

	if err := a.deleteRouteTables(ctx, vpcId, client); err != nil {
		LogError("failed to delete route tables for VPC %s: %s", vpcId, err.Error())
	}

	if err := a.deleteSubnets(ctx, vpcId, client); err != nil {
		LogError("failed to delete subnets for VPC %s: %s", vpcId, err.Error())
	}

	return nil
}

func (a *action) deleteNATGateways(ctx context.Context, vpcId string, client *ec2.EC2) error {
	resp, err := client.DescribeNatGatewaysWithContext(ctx, &ec2.DescribeNatGatewaysInput{
		Filter: []*ec2.Filter{
			{Name: aws.String("vpc-id"), Values: []*string{&vpcId}},
			{Name: aws.String("state"), Values: []*string{aws.String("available")}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to describe NAT gateways: %w", err)
	}

	for _, natGw := range resp.NatGateways {
		LogDebug("Deleting NAT Gateway %s", *natGw.NatGatewayId)
		if _, err := client.DeleteNatGatewayWithContext(ctx, &ec2.DeleteNatGatewayInput{
			NatGatewayId: natGw.NatGatewayId,
		}); err != nil {
			LogError("failed to delete NAT gateway %s: %s", *natGw.NatGatewayId, err.Error())
		}
	}

	return nil
}

func (a *action) deleteInternetGateways(ctx context.Context, vpcId string, client *ec2.EC2) error {
	resp, err := client.DescribeInternetGatewaysWithContext(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("attachment.vpc-id"), Values: []*string{&vpcId}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to describe internet gateways: %w", err)
	}

	for _, igw := range resp.InternetGateways {
		LogDebug("Detaching and deleting Internet Gateway %s", *igw.InternetGatewayId)

		if _, err := client.DetachInternetGatewayWithContext(ctx, &ec2.DetachInternetGatewayInput{
			InternetGatewayId: igw.InternetGatewayId,
			VpcId:             &vpcId,
		}); err != nil {
			LogError("failed to detach internet gateway %s: %s", *igw.InternetGatewayId, err.Error())
			continue
		}

		if _, err := client.DeleteInternetGatewayWithContext(ctx, &ec2.DeleteInternetGatewayInput{
			InternetGatewayId: igw.InternetGatewayId,
		}); err != nil {
			LogError("failed to delete internet gateway %s: %s", *igw.InternetGatewayId, err.Error())
		}
	}

	return nil
}

func (a *action) deleteRouteTables(ctx context.Context, vpcId string, client *ec2.EC2) error {
	resp, err := client.DescribeRouteTablesWithContext(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("vpc-id"), Values: []*string{&vpcId}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to describe route tables: %w", err)
	}

	for _, rt := range resp.RouteTables {
		isMain := false
		for _, assoc := range rt.Associations {
			if aws.BoolValue(assoc.Main) {
				isMain = true
				break
			}
		}
		if isMain {
			LogDebug("Skipping main route table %s", *rt.RouteTableId)
			continue
		}

		LogDebug("Deleting route table %s", *rt.RouteTableId)
		if _, err := client.DeleteRouteTableWithContext(ctx, &ec2.DeleteRouteTableInput{
			RouteTableId: rt.RouteTableId,
		}); err != nil {
			LogError("failed to delete route table %s: %s", *rt.RouteTableId, err.Error())
		}
	}

	return nil
}

func (a *action) deleteSubnets(ctx context.Context, vpcId string, client *ec2.EC2) error {
	resp, err := client.DescribeSubnetsWithContext(ctx, &ec2.DescribeSubnetsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("vpc-id"), Values: []*string{&vpcId}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to describe subnets: %w", err)
	}

	for _, subnet := range resp.Subnets {
		LogDebug("Deleting subnet %s", *subnet.SubnetId)
		if _, err := client.DeleteSubnetWithContext(ctx, &ec2.DeleteSubnetInput{
			SubnetId: subnet.SubnetId,
		}); err != nil {
			LogError("failed to delete subnet %s: %s", *subnet.SubnetId, err.Error())
		}
	}

	return nil
}
