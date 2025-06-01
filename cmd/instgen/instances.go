package main

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// InstanceTypes wraps consumption of all paginated `DescribeInstanceTypes`
// results into an iterator.
func InstanceTypes(
	ctx context.Context,
	client *ec2.Client,
	input *ec2.DescribeInstanceTypesInput,
) InstanceIter {
	return func(yield func(*types.InstanceTypeInfo) bool) {
		if client == nil || input == nil {
			return
		}

		// Fetch the next round of instance types
		output, err := client.DescribeInstanceTypes(ctx, input)
		if err != nil {
			log.Fatalf("Failed to describe AWS instance types: %s.", err)
		}

		// Send 'em all up the chute
		for _, itype := range output.InstanceTypes {
			if !yield(&itype) {
				return
			}
		}

		// Break the cycle if we do not have a next token
		if output.NextToken == nil {
			return
		}

		// Recurse if we have a next token
		input.NextToken = output.NextToken
		for t := range InstanceTypes(ctx, client, input) {
			if !yield(t) {
				return
			}
		}
	}
}
