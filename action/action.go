package action

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/eks"
	"github.com/aws/aws-sdk-go/service/elb"
)

type AwsJanitorAction interface {
	Cleanup(ctx context.Context, input *Input) error
}

func New(commit bool) AwsJanitorAction {
	return &action{
		commit: commit,
	}
}

type action struct {
	commit bool
}

type Cleaner struct {
	Service string
	Run     CleanupFunc
}

func (a *action) Cleanup(ctx context.Context, input *Input) error {

	// use []Cleaner to keep the order
	cleaners := []Cleaner{
		{Service: eks.ServiceName, Run: a.cleanEKSClusters},
		{Service: autoscaling.ServiceName, Run: a.cleanASGs},
		{Service: elb.ServiceName, Run: a.cleanLoadBalancers},
		{Service: ec2.ServiceName, Run: a.cleanSecurityGroups},
		{Service: cloudformation.ServiceName, Run: a.cleanCfStacks},
	}
	inputRegions := strings.Split(input.Regions, ",")

	for _, cleaner := range cleaners {
		regions := getServiceRegions(cleaner.Service, inputRegions)

		for _, region := range regions {
			sess, err := session.NewSession(&aws.Config{
				Region: aws.String(region)},
			)
			if err != nil {
				return fmt.Errorf("failed to create aws session for region %s: %w", region, err)
			}

			scope := &CleanupScope{
				Session:   sess,
				Commit:    input.Commit,
				IgnoreTag: input.IgnoreTag,
			}

			Log("Cleaning up resources for service %s in region %s", cleaner.Service, region)
			if err := cleaner.Run(ctx, scope); err != nil {
				return fmt.Errorf("failed running cleanup for service %s: %w", cleaner.Service, err)
			}
		}
	}

	return nil
}

func getServiceRegions(service string, inputRegions []string) []string {
	regions := []string{}
	allRegions := inputRegions[0] == "*"

	sr, exists := endpoints.RegionsForService(endpoints.DefaultPartitions(), endpoints.AwsPartitionID, service)
	if exists {
		for _, region := range sr {
			if allRegions {
				regions = append(regions, region.ID())
			} else {
				for _, r := range inputRegions {
					if r == region.ID() {
						regions = append(regions, region.ID())
					}
				}
			}
		}
	}

	return regions
}
