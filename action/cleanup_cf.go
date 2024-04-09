package action

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	cf "github.com/aws/aws-sdk-go/service/cloudformation"
)

func (a *action) cleanCfStacks(ctx context.Context, input *CleanupScope) error {
	client := cf.New(input.Session)

	stacksToDelete := []*string{}
	pageFunc := func(page *cf.DescribeStacksOutput, _ bool) bool {
		for _, stack := range page.Stacks {
			var ignore, markedForDeletion bool
			for _, tag := range stack.Tags {
				if *tag.Key == input.IgnoreTag {
					ignore = true
				} else if *tag.Key == DeletionTag {
					markedForDeletion = true
				}
			}

			if ignore {
				LogDebug("cloudformation stack %s has ignore tag, skipping cleanup", *stack.StackName)
				continue
			}

			if !markedForDeletion {
				// NOTE: only mark for future deletion if we're not running in dry-mode
				if a.commit {
					LogDebug("cloudformation stack %s does not have deletion tag, marking for future deletion and skipping cleanup", *stack.StackName)
					if err := a.markCfStackForFutureDeletion(ctx, stack, client); err != nil {
						LogError("failed to mark cloudformation stack %s for future deletion: %s", *stack.StackName, err.Error())
					}
				}
				continue
			}

			switch aws.StringValue(stack.StackStatus) {
			case cf.ResourceStatusDeleteComplete,
				cf.ResourceStatusDeleteInProgress:
				LogDebug("cloudformation stack %s is already deleted/deleting, skipping cleanup", *stack.StackName)
				continue
			}

			LogDebug("adding cloudformation stack %s to delete list", *stack.StackName)
			stacksToDelete = append(stacksToDelete, stack.StackName)
		}

		return true
	}

	if err := client.DescribeStacksPagesWithContext(ctx, &cf.DescribeStacksInput{}, pageFunc); err != nil {
		return fmt.Errorf("failed getting list of cloudformation stacks: %w", err)
	}

	if len(stacksToDelete) == 0 {
		Log("no cloudformation stacks to delete")
		return nil
	}

	for _, stackName := range stacksToDelete {
		if !a.commit {
			LogDebug("skipping deletion of cloudformation stack %s as running in dry-mode", *stackName)
			continue
		}

		if err := a.deleteCfStack(ctx, *stackName, client); err != nil {
			LogError("failed to delete cloudformation stack %s: %s", *stackName, err.Error())
		}
	}

	return nil
}

func (a *action) markCfStackForFutureDeletion(ctx context.Context, stack *cf.Stack, client *cf.CloudFormation) error {
	Log("Marking CloudFormation stack %s for future deletion", *stack.StackName)

	stack.SetTags(append(stack.Tags, &cf.Tag{Key: aws.String(DeletionTag), Value: aws.String("true")}))

	LogDebug("Updating tags for cloudformation stack %s", *stack.StackName)

	if _, err := client.UpdateStackWithContext(ctx, &cf.UpdateStackInput{
		Capabilities:        stack.Capabilities,
		StackName:           stack.StackName,
		Tags:                stack.Tags,
		UsePreviousTemplate: aws.Bool(true),
	}); err != nil {
		return fmt.Errorf("failed to update cloudformation stack %s: %w", *stack.StackName, err)
	}

	if err := client.WaitUntilStackUpdateCompleteWithContext(ctx, &cf.DescribeStacksInput{StackName: stack.StackName}); err != nil {
		return fmt.Errorf("failed to wait for cloudformation stack %s to update: %w", *stack.StackName, err)
	}

	return nil
}

func (a *action) deleteCfStack(ctx context.Context, stackName string, client *cf.CloudFormation) error {
	Log("Deleting CloudFormation stack %s", stackName)

	if _, err := client.DeleteStackWithContext(ctx, &cf.DeleteStackInput{StackName: &stackName}); err != nil {
		return fmt.Errorf("failed to delete cloudformation stack %s: %w", stackName, err)
	}

	if err := client.WaitUntilStackDeleteCompleteWithContext(ctx, &cf.DescribeStacksInput{StackName: &stackName}); err != nil {
		return fmt.Errorf("failed to wait for cloudformation stack %s to delete: %w", stackName, err)
	}

	return nil
}
