package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// instance is a discovered, running EC2 target.
type instance struct {
	id   string
	name string // Name tag, falling back to id
}

// discover resolves running instances matching every supplied tag (AND-combined).
func discover(ctx context.Context, client *ec2.Client, tags []tag) ([]instance, error) {
	filters := make([]ec2types.Filter, 0, len(tags)+1)
	for _, t := range tags {
		filters = append(filters, ec2types.Filter{
			Name:   aws.String("tag:" + t.key),
			Values: []string{t.value},
		})
	}
	filters = append(filters, ec2types.Filter{
		Name:   aws.String("instance-state-name"),
		Values: []string{"running"},
	})

	paginator := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{Filters: filters})

	var instances []instance
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("DescribeInstances: %w", err)
		}
		for _, res := range page.Reservations {
			for _, inst := range res.Instances {
				id := aws.ToString(inst.InstanceId)
				instances = append(instances, instance{id: id, name: nameOf(inst, id)})
			}
		}
	}
	return instances, nil
}

// nameOf returns the instance's Name tag, or the instance id when the tag is absent.
func nameOf(inst ec2types.Instance, id string) string {
	for _, t := range inst.Tags {
		if aws.ToString(t.Key) == "Name" {
			if v := aws.ToString(t.Value); v != "" {
				return v
			}
		}
	}
	return id
}
