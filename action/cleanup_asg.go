package action

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
)

func (a *action) cleanASGs(ctx context.Context, input *CleanupScope) error {
	client := autoscaling.New(input.Session)

	asgToDelete := []*autoscaling.Group{}
	pageFunc := func(page *autoscaling.DescribeAutoScalingGroupsOutput, _ bool) bool {
		for _, asg := range page.AutoScalingGroups {
			var ignore, markedForDeletion bool
			for _, tag := range asg.Tags {
				switch *tag.Key {
				case input.IgnoreTag:
					ignore = true
				case DeletionTag:
					markedForDeletion = true
				}
			}

			if ignore {
				LogDebug("asg %s has ignore tag, skipping cleanup", *asg.AutoScalingGroupName)
				continue
			}

			if !markedForDeletion {
				// NOTE: only mark for future deletion if we're not running in dry-mode
				if a.commit {
					LogDebug("asg %s does not have deletion tag, marking for future deletion and skipping cleanup", *asg.AutoScalingGroupName)
					if err := a.markAsgForFutureDeletion(ctx, *asg.AutoScalingGroupName, client); err != nil {
						LogError("failed to mark asg %s for future deletion: %s", *asg.AutoScalingGroupName, err.Error())
					}
				}
				continue
			}

			LogDebug("adding asg %s to delete list", *asg.AutoScalingGroupName)
			asgToDelete = append(asgToDelete, asg)
		}

		return true
	}

	if err := client.DescribeAutoScalingGroupsPagesWithContext(ctx, &autoscaling.DescribeAutoScalingGroupsInput{MaxRecords: aws.Int64(100)}, pageFunc); err != nil {
		return fmt.Errorf("failed to get asgs: %w", err)
	}

	if len(asgToDelete) == 0 {
		Log("no autoscaling groups to delete")
		return nil
	}

	deletedNames := []*string{}
	for _, asg := range asgToDelete {
		if !a.commit {
			LogDebug("skipping deletion of asg %s as running in dry-mode", *asg.AutoScalingGroupName)
			return nil
		}

		Log("Deleting asg %s", *asg.AutoScalingGroupName)
		if _, err := client.DeleteAutoScalingGroupWithContext(ctx, &autoscaling.DeleteAutoScalingGroupInput{AutoScalingGroupName: asg.AutoScalingGroupName}); err != nil {
			LogError("failed to delete asg %s: %s", *asg.AutoScalingGroupName, err.Error())
			continue
		}

		deletedNames = append(deletedNames, asg.AutoScalingGroupName)
	}

	if len(deletedNames) > 0 {
		if err := client.WaitUntilGroupNotExistsWithContext(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: deletedNames,
		}); err != nil {
			LogError("failed to wait for asg to be deleted: %s", err.Error())
		}
	}

	return nil
}

func (a *action) markAsgForFutureDeletion(ctx context.Context, asgName string, client *autoscaling.AutoScaling) error {
	Log("Marking ASG %s for future deletion", asgName)

	_, err := client.CreateOrUpdateTagsWithContext(ctx, &autoscaling.CreateOrUpdateTagsInput{Tags: []*autoscaling.Tag{
		{
			Key:               aws.String(DeletionTag),
			PropagateAtLaunch: aws.Bool(true),
			ResourceId:        aws.String(asgName),
			ResourceType:      aws.String("auto-scaling-group"),
			Value:             aws.String("true"),
		},
	}})

	return err
}
