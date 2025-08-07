package action

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

func (a *action) cleanSecurityGroups(ctx context.Context, input *CleanupScope) error {
	client := ec2.New(input.Session)

	sgsToDelete := []*ec2.SecurityGroup{}
	// NOTE: we delete security groups based on whether we're later deleting the vpc they belong to or not.
	pageFunc := func(page *ec2.DescribeVpcsOutput, _ bool) bool {
		sgPageFunc := func(sgPage *ec2.GetSecurityGroupsForVpcOutput, _ bool) bool {
			for _, sg := range sgPage.SecurityGroupForVpcs {
				var ignore, markedForDeletion bool
				for _, tag := range sg.Tags {
					if *tag.Key == input.IgnoreTag {
						ignore = true
					} else if *tag.Key == DeletionTag {
						markedForDeletion = true
					}
				}

				if ignore || *sg.GroupName == "default" {
					LogDebug("security group %s has ignore tag or is a default security group, skipping cleanup", *sg.GroupId)
					continue
				}

				if !markedForDeletion {
					// NOTE: only mark for future deletion if we're not running in dry-mode
					if a.commit {
						LogDebug("security group %s does not have deletion tag, marking for future deletion and skipping cleanup", *sg.GroupId)
						if err := a.markSecurityGroupForFutureDeletion(ctx, *sg.GroupId, client); err != nil {
							LogError("failed to mark security group %s for future deletion: %s", *sg.GroupId, err.Error())
						}
					}
					continue
				}

				securityGroups, err := client.DescribeSecurityGroupsWithContext(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []*string{sg.GroupId}})
				if err != nil || len(securityGroups.SecurityGroups) != 1 {
					LogError("failed to describe security group %s: %s", *sg.GroupId, err.Error())
					continue
				}

				LogDebug("adding security group %s to delete list", *sg.GroupId)
				sgsToDelete = append(sgsToDelete, securityGroups.SecurityGroups[0])
			}

			return true
		}

		for _, vpc := range page.Vpcs {
			var ignore bool
			for _, tag := range vpc.Tags {
				if *tag.Key == input.IgnoreTag {
					ignore = true
					break
				}
			}

			if ignore || aws.BoolValue(vpc.IsDefault) {
				LogDebug("vpc %s has ignore tag or is a default vpc, won't delete security groups associated with it", *vpc.VpcId)
				continue
			}

			if err := client.GetSecurityGroupsForVpcPagesWithContext(ctx, &ec2.GetSecurityGroupsForVpcInput{VpcId: vpc.VpcId}, sgPageFunc); err != nil {
				LogError("failed getting list of security groups for vpc %s: %s", *vpc.VpcId, err.Error())
				continue
			}

		}

		return true
	}

	if err := client.DescribeVpcsPagesWithContext(ctx, &ec2.DescribeVpcsInput{}, pageFunc); err != nil {
		return fmt.Errorf("failed getting list of vpcs: %w", err)
	}

	if len(sgsToDelete) == 0 {
		Log("no security groups to delete")
		return nil
	}

	// NOTE: some security groups may have rules that reference other security groups.
	// deleting a security group that's referenced in another's rules will fail,
	// so we need to delete the rules first.
	for _, securityGroup := range sgsToDelete {
		if !a.commit {
			LogDebug("skipping deletion of security group %s as running in dry-mode", *securityGroup.GroupId)
			continue
		}

		if err := a.deleteSecurityGroupRules(ctx, *securityGroup.GroupId, securityGroup.IpPermissions, securityGroup.IpPermissionsEgress, client); err != nil {
			LogError("failed to delete security group rules for %s: %s", *securityGroup.GroupId, err.Error())
		}

	}

	for _, securityGroup := range sgsToDelete {
		if !a.commit {
			LogDebug("skipping deletion of security group %s as running in dry-mode", *securityGroup.GroupId)
			continue
		}

		LogDebug("Sleeping for 10 seconds to allow AWS to catch up")
		time.Sleep(10 * time.Second)

		if err := a.deleteSecurityGroup(ctx, *securityGroup.GroupId, client); err != nil {
			LogError("failed to delete security group %s: %s", *securityGroup.GroupId, err.Error())
		}
	}

	return nil
}

func (a *action) markSecurityGroupForFutureDeletion(ctx context.Context, sgId string, client *ec2.EC2) error {
	Log("Marking Security Group %s for future deletion", sgId)

	_, err := client.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
		Resources: []*string{&sgId}, Tags: []*ec2.Tag{
			{Key: aws.String(DeletionTag), Value: aws.String("true")},
		},
	})

	return err
}

func (a *action) deleteSecurityGroupRules(ctx context.Context, sgId string, sgIngress, sgEgress []*ec2.IpPermission, client *ec2.EC2) error {
	Log("Deleting Ingress/Egress Rules from security group %s", sgId)

	if len(sgIngress) != 0 {
		if _, err := client.RevokeSecurityGroupIngressWithContext(ctx, &ec2.RevokeSecurityGroupIngressInput{GroupId: &sgId, IpPermissions: sgIngress}); err != nil {
			return fmt.Errorf("failed to revoke ingress rules from security group %s: %w", sgId, err)
		}
	}

	if len(sgEgress) != 0 {
		if _, err := client.RevokeSecurityGroupEgressWithContext(ctx, &ec2.RevokeSecurityGroupEgressInput{GroupId: &sgId, IpPermissions: sgEgress}); err != nil {
			return fmt.Errorf("failed to revoke egress rules from security group %s: %w", sgId, err)
		}
	}

	return nil
}

func (a *action) deleteSecurityGroup(ctx context.Context, sgId string, client *ec2.EC2) error {
	Log("Deleting Security Group %s", sgId)

	maxRetries := 5
	retryDelay := 30 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if _, err := client.DeleteSecurityGroupWithContext(ctx, &ec2.DeleteSecurityGroupInput{GroupId: &sgId}); err != nil {
			if attempt < maxRetries && a.isDependencyViolation(err) {
				LogDebug("Security group %s has dependencies, retrying in %v (attempt %d/%d)", sgId, retryDelay, attempt, maxRetries)

				if err := a.handleSecurityGroupDependencies(ctx, sgId, client); err != nil {
					LogDebug("Failed to handle dependencies for security group %s: %v", sgId, err)
				}

				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("failed to delete security group %s: %w", sgId, err)
		}
		break
	}

	return nil
}

func (a *action) isDependencyViolation(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "DependencyViolation") ||
		strings.Contains(errStr, "has a dependent object")
}

func (a *action) handleSecurityGroupDependencies(ctx context.Context, sgId string, client *ec2.EC2) error {
	eniResp, err := client.DescribeNetworkInterfacesWithContext(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("group-id"),
				Values: []*string{aws.String(sgId)},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to describe network interfaces for sg %s: %w", sgId, err)
	}

	for _, eni := range eniResp.NetworkInterfaces {
		LogDebug("Security group %s is used by network interface %s (status: %s)",
			sgId, aws.StringValue(eni.NetworkInterfaceId), aws.StringValue(eni.Status))

		if aws.StringValue(eni.Status) == "available" {
			LogDebug("Network interface %s is available but not being deleted automatically for safety",
				aws.StringValue(eni.NetworkInterfaceId))
		}
	}

	return nil
}
