package ec2

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// tagSpecificationWithDefaults produces a tag specification where the 'defaultTags'
// values are appended to the end of the 'withTags' variadic input values.
//
// A 'TagSpecification' is just AWS' term for metadata, defined as key-value
// pairs, associated with a particular 'types.ResourceType'. Examples of these
// 'ResourceType's would be EC2 instances, VPCs, VPC subnets and so on.
func tagSpecificationWithDefaults(rt types.ResourceType, withTags ...types.Tag) []types.TagSpecification {
	return []types.TagSpecification{
		{
			ResourceType: rt,
			Tags:         append(withTags, tagsDefault()...),
		},
	}
}

const (
	// These are some well-known AWS tag keys.
	//
	// 'Name' is well-known within AWS itself, the rest are well-known Chainguard
	// internal tag keys (well-known as in these are commonly used in Terraform).
	tagKeyName    = "Name"
	tagKeyTeam    = "Team"
	tagKeyProject = "Project"

	// These are default values, where applicable, corresponding to the above tag
	// keys.
	tagDefaultTeam    = "Containers"
	tagDefaultProject = "terraform-provider-imagetest::driver::ec2"
)

// tagsDefault produces the standard key-value pairs which should be associated
// to all created EC2 resources.
//
// This should probably only ever be called by 'defaultTagSpecification'.
func tagsDefault() []types.Tag {
	return []types.Tag{
		{
			Key:   aws.String(tagKeyTeam),
			Value: aws.String(tagDefaultTeam),
		},
		{
			Key:   aws.String(tagKeyProject),
			Value: aws.String(tagDefaultProject),
		},
	}
}
