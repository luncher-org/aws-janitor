package action

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

func (a *action) cleanNetworkInterfaces(ctx context.Context, input *CleanupScope) error {
	client := ec2.New(input.Session)

	out, err := client.DescribeNetworkInterfacesWithContext(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("status"), Values: []*string{aws.String("available")}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to describe network interfaces: %w", err)
	}

	if len(out.NetworkInterfaces) == 0 {
		Log("no unattached network interfaces to delete")
		return nil
	}

	for _, ni := range out.NetworkInterfaces {
		var ignore, markedForDeletion bool
		for _, tag := range ni.TagSet {
			switch aws.StringValue(tag.Key) {
			case input.IgnoreTag:
				ignore = true
			case DeletionTag:
				markedForDeletion = true
			}
		}
		if ignore {
			LogDebug("network interface %s has ignore tag, skipping cleanup", aws.StringValue(ni.NetworkInterfaceId))
			continue
		}

		if !markedForDeletion {
			if a.commit {
				LogDebug("network interface %s does not have deletion tag, marking for future deletion and skipping cleanup", aws.StringValue(ni.NetworkInterfaceId))
				if _, err := client.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
					Resources: []*string{ni.NetworkInterfaceId},
					Tags:      []*ec2.Tag{{Key: aws.String(DeletionTag), Value: aws.String("true")}},
				}); err != nil {
					LogError("failed to mark network interface %s for future deletion: %s", aws.StringValue(ni.NetworkInterfaceId), err.Error())
				}
			}
			continue
		}

		if !a.commit {
			LogDebug("skipping deletion of network interface %s as running in dry-mode", aws.StringValue(ni.NetworkInterfaceId))
			continue
		}

		Log("Deleting unattached network interface %s (subnet %s, desc=%s)", aws.StringValue(ni.NetworkInterfaceId), aws.StringValue(ni.SubnetId), aws.StringValue(ni.Description))
		if _, err := client.DeleteNetworkInterfaceWithContext(ctx, &ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: ni.NetworkInterfaceId}); err != nil {
			LogWarning("failed to delete network interface %s: %s", aws.StringValue(ni.NetworkInterfaceId), err.Error())
			continue
		}
	}

	return nil
}
