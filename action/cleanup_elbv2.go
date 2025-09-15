package action

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elbv2"
)

func (a *action) cleanLoadBalancersV2(ctx context.Context, input *CleanupScope) error {
	client := elbv2.New(input.Session)

	lbsToDelete := []*string{}

	pageFunc := func(page *elbv2.DescribeLoadBalancersOutput, _ bool) bool {
		for _, lb := range page.LoadBalancers {
			tagOut, err := client.DescribeTagsWithContext(ctx, &elbv2.DescribeTagsInput{ResourceArns: []*string{lb.LoadBalancerArn}})
			if err != nil {
				LogError("failed getting tags for elbv2 %s: %s", aws.StringValue(lb.LoadBalancerName), err.Error())
				continue
			}

			var ignore, markedForDeletion bool
			for _, desc := range tagOut.TagDescriptions {
				for _, tag := range desc.Tags {
					switch aws.StringValue(tag.Key) {
					case input.IgnoreTag:
						ignore = true
					case DeletionTag:
						markedForDeletion = true
					}
				}
			}

			if ignore {
				LogDebug("elbv2 %s has ignore tag, skipping cleanup", aws.StringValue(lb.LoadBalancerName))
				continue
			}

			if !markedForDeletion {
				if a.commit {
					LogDebug("elbv2 %s does not have deletion tag, marking for future deletion and skipping cleanup", aws.StringValue(lb.LoadBalancerName))
					if err := a.markLoadBalancerV2ForFutureDeletion(ctx, aws.StringValue(lb.LoadBalancerArn), client); err != nil {
						LogError("failed to mark elbv2 %s for future deletion: %s", aws.StringValue(lb.LoadBalancerName), err.Error())
					}
				}
				continue
			}

			LogDebug("adding elbv2 %s to delete list", aws.StringValue(lb.LoadBalancerName))
			lbsToDelete = append(lbsToDelete, lb.LoadBalancerArn)
		}

		return true
	}

	if err := client.DescribeLoadBalancersPagesWithContext(ctx, &elbv2.DescribeLoadBalancersInput{}, pageFunc); err != nil {
		return fmt.Errorf("failed getting list of elbv2 load balancers: %w", err)
	}

	if len(lbsToDelete) == 0 {
		Log("no elbv2 load balancers to delete")
		return nil
	}

	for _, arn := range lbsToDelete {
		if !a.commit {
			LogDebug("skipping deletion of elbv2 %s as running in dry-mode", aws.StringValue(arn))
			continue
		}

		if err := a.deleteLoadBalancerV2(ctx, aws.StringValue(arn), client); err != nil {
			LogError("failed to delete elbv2 %s: %s", aws.StringValue(arn), err.Error())
		}
	}

	return nil
}

func (a *action) deleteLoadBalancerV2(ctx context.Context, lbArn string, client *elbv2.ELBV2) error {
	Log("Deleting ELBv2 %s and its target groups", lbArn)

	tgsOut, err := client.DescribeTargetGroupsWithContext(ctx, &elbv2.DescribeTargetGroupsInput{LoadBalancerArn: aws.String(lbArn)})
	if err != nil {
		LogWarning("failed to list target groups for lb %s: %s", lbArn, err.Error())
	}

	if _, err := client.DeleteLoadBalancerWithContext(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(lbArn)}); err != nil {
		return fmt.Errorf("failed to delete elbv2 %s: %w", lbArn, err)
	}

	if err := client.WaitUntilLoadBalancersDeletedWithContext(ctx, &elbv2.DescribeLoadBalancersInput{LoadBalancerArns: []*string{aws.String(lbArn)}}); err != nil {
		LogWarning("failed waiting for elbv2 %s deletion: %s", lbArn, err.Error())
	}

	for _, tg := range tgsOut.TargetGroups {
		Log("Deleting target group %s", aws.StringValue(tg.TargetGroupArn))
		if _, err := client.DeleteTargetGroupWithContext(ctx, &elbv2.DeleteTargetGroupInput{TargetGroupArn: tg.TargetGroupArn}); err != nil {
			LogWarning("failed to delete target group %s: %s", aws.StringValue(tg.TargetGroupArn), err.Error())
		}
	}

	return nil
}

func (a *action) markLoadBalancerV2ForFutureDeletion(ctx context.Context, lbArn string, client *elbv2.ELBV2) error {
	Log("Marking ELBv2 %s for future deletion", lbArn)
	_, err := client.AddTagsWithContext(ctx, &elbv2.AddTagsInput{
		ResourceArns: []*string{aws.String(lbArn)},
		Tags:         []*elbv2.Tag{{Key: aws.String(DeletionTag), Value: aws.String("true")}},
	})
	return err
}
