package action

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/eks"
)

func (a *action) cleanEKSClusters(ctx context.Context, input *CleanupScope) error {
	client := eks.New(input.Session)

	clustersToDelete := []*eks.Cluster{}
	pageFunc := func(page *eks.ListClustersOutput, _ bool) bool {
		for _, name := range page.Clusters {
			cluster, err := client.DescribeClusterWithContext(ctx, &eks.DescribeClusterInput{
				Name: name,
			})
			if err != nil {
				LogWarning("failed getting cluster %s: %s", *name, err.Error())
				continue
			}

			if _, ok := cluster.Cluster.Tags[input.IgnoreTag]; ok {
				LogDebug("eks cluster %s has ignore tag, skipping cleanup", *name)
				continue
			}

			if _, ok := cluster.Cluster.Tags[DeletionTag]; !ok {
				// NOTE: only mark for future deletion if we're not running in dry-mode
				if a.commit {
					LogDebug("eks cluster %s does not have deletion tag, marking for future deletion and skipping cleanup", *name)
					if err := a.markEKSClusterForFutureDeletion(ctx, *cluster.Cluster.Arn, client); err != nil {
						LogError("failed to mark cluster %s for future deletion: %s", *cluster.Cluster.Arn, err.Error())
					}
				}
				continue
			}

			LogDebug("adding eks cluster %s to delete list", *name)
			clustersToDelete = append(clustersToDelete, cluster.Cluster)
		}

		return true
	}

	if err := client.ListClustersPagesWithContext(ctx, &eks.ListClustersInput{}, pageFunc); err != nil {
		return fmt.Errorf("failed getting list of eks clusters: %w", err)
	}

	if len(clustersToDelete) == 0 {
		Log("no eks clusters to delete")
		return nil
	}

	for _, clusterObj := range clustersToDelete {
		if !a.commit {
			LogDebug("skipping deletion of eks cluster %s as running in dry-mode", *clusterObj.Name)
			continue
		}

		if err := a.deleteEKSCluster(ctx, *clusterObj.Name, client); err != nil {
			LogError("failed to delete cluster %s: %s", *clusterObj.Name, err.Error())
		}
	}

	return nil
}

func (a *action) markEKSClusterForFutureDeletion(ctx context.Context, clusterArn string, client *eks.EKS) error {
	Log("Marking EKS cluster %s for future deletion", clusterArn)

	_, err := client.TagResourceWithContext(ctx, &eks.TagResourceInput{ResourceArn: &clusterArn, Tags: map[string]*string{DeletionTag: aws.String("true")}})

	return err
}

func (a *action) deleteEKSCluster(ctx context.Context, clusterName string, client *eks.EKS) error {
	Log("Deleting EKS cluster %s", clusterName)

	LogDebug("Deleting nodegroups for cluster %s", clusterName)

	listErr := client.ListNodegroupsPagesWithContext(ctx, &eks.ListNodegroupsInput{ClusterName: &clusterName}, func(page *eks.ListNodegroupsOutput, _ bool) bool {
		for _, ngName := range page.Nodegroups {
			Log("Deleting nodegroup %s in cluster %s", *ngName, clusterName)
			if _, err := client.DeleteNodegroupWithContext(ctx, &eks.DeleteNodegroupInput{ClusterName: &clusterName, NodegroupName: ngName}); err != nil {
				LogError("failed to deleted nodegroups %s for cluster %s: %s", *ngName, clusterName, err.Error())
			}

			if err := client.WaitUntilNodegroupDeletedWithContext(ctx, &eks.DescribeNodegroupInput{ClusterName: &clusterName, NodegroupName: ngName}); err != nil {
				LogError("failed to wait for nodegroups %s in cluster %s to be deleted: %s", *ngName, clusterName, err.Error())
			}
		}

		return true
	})
	if listErr != nil {
		return fmt.Errorf("failed to list nodegroups for cluster %s: %w", clusterName, listErr)
	}

	if _, err := client.DeleteClusterWithContext(ctx, &eks.DeleteClusterInput{Name: &clusterName}); err != nil {
		return fmt.Errorf("failed to delete cluster %s: %w", clusterName, err)
	}

	if err := client.WaitUntilClusterDeletedWithContext(ctx, &eks.DescribeClusterInput{Name: &clusterName}); err != nil {
		return fmt.Errorf("failed to wait for cluster %s to be delete: %w", clusterName, err)
	}

	return nil
}
