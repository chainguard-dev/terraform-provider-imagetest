package ec2

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func defaultTags() []types.Tag {
	return []types.Tag{
		{
			Key:   aws.String("team"),
			Value: aws.String("Containers"),
		},
		{
			Key:   aws.String("project"),
			Value: aws.String("terraform-provider-imagetest/ec2-driver"),
		},
	}
}
