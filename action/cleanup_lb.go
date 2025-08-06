package action

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elb"
)

func (a *action) cleanLoadBalancers(ctx context.Context, input *CleanupScope) error {
	client := elb.New(input.Session)

	loadBalancersToDelete := []*string{}
	pageFunc := func(page *elb.DescribeLoadBalancersOutput, _ bool) bool {
		for _, lb := range page.LoadBalancerDescriptions {
			tags, err := client.DescribeTagsWithContext(ctx, &elb.DescribeTagsInput{LoadBalancerNames: []*string{lb.LoadBalancerName}})
			if err != nil {
				LogError("failed getting tags for load balancer %s: %s", *lb.LoadBalancerName, err.Error())
			}

			var ignore, markedForDeletion bool
			for _, tagDescription := range tags.TagDescriptions {
				for _, tag := range tagDescription.Tags {
					switch *tag.Key {
					case input.IgnoreTag:
						ignore = true
					case DeletionTag:
						markedForDeletion = true
					}
				}
			}

			if ignore {
				LogDebug("load balancer %s has ignore tag, skipping cleanup", *lb.LoadBalancerName)
				continue
			}

			if !markedForDeletion {
				// NOTE: only mark for future deletion if we're not running in dry-mode
				if a.commit {
					LogDebug("load balancer %s does not have deletion tag, marking for future deletion and skipping cleanup", *lb.LoadBalancerName)
					if err := a.markLoadBalancerForFutureDeletion(ctx, *lb.LoadBalancerName, client); err != nil {
						LogError("failed to mark load balancer %s for future deletion: %s", *lb.LoadBalancerName, err.Error())
					}
				}
				continue
			}

			LogDebug("adding load balancer %s to delete list", *lb.LoadBalancerName)
			loadBalancersToDelete = append(loadBalancersToDelete, lb.LoadBalancerName)
		}

		return true
	}

	if err := client.DescribeLoadBalancersPagesWithContext(ctx, &elb.DescribeLoadBalancersInput{}, pageFunc); err != nil {
		return fmt.Errorf("failed getting list of load balancer: %w", err)
	}

	if len(loadBalancersToDelete) == 0 {
		Log("no load balancer to delete")
		return nil
	}

	for _, lbName := range loadBalancersToDelete {
		if !a.commit {
			LogDebug("skipping deletion of load balancer %s as running in dry-mode", *lbName)
			continue
		}

		if err := a.deleteLoadBalancer(ctx, *lbName, client); err != nil {
			LogError("failed to delete load balancer %s: %s", *lbName, err.Error())
		}
	}

	return nil
}
func (a *action) markLoadBalancerForFutureDeletion(ctx context.Context, lbName string, client *elb.ELB) error {
	Log("Marking Load Balancer %s for future deletion", lbName)

	_, err := client.AddTagsWithContext(ctx, &elb.AddTagsInput{
		LoadBalancerNames: []*string{&lbName},
		Tags: []*elb.Tag{
			{
				Key:   aws.String(DeletionTag),
				Value: aws.String("true")},
		},
	})

	return err
}

func (a *action) deleteLoadBalancer(ctx context.Context, lbName string, client *elb.ELB) error {
	Log("Deleting Load Balancer %s", lbName)

	if _, err := client.DeleteLoadBalancerWithContext(ctx, &elb.DeleteLoadBalancerInput{LoadBalancerName: &lbName}); err != nil {
		return fmt.Errorf("failed to delete load balancer %s: %w", lbName, err)
	}

	return nil
}
