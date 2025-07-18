// filter provides composable functions for filtering EC2 instance types.
//
// The AWS SDK v2's in-request filtering of EC2 instance types is _really_ rough
// to work with. There are a few cases where the in-request filter outright
// doesn't work and many more where a filter simply does not exist for criteria
// we need to select based on. This package aims to solve the former, implement
// support for the latter and add a touch of composability to both.
//
// # Pre vs. Post
//
// This package breaks down to two primary structs: 'Pre' and 'Post'.
//
// 'Pre' wraps filters which are supported by the AWS SDK, allowing a cleaner
// composition that's easier to look at, tweak and extend. These filters should
// be applied via the 'Filters' slice on the appropriate 'ec2' package type.
//
// 'Post' implements filters which are _not_ supported by the AWS SDK. These
// filters should be applied to the slice of 'ec2/types.InstanceTypeInfo'
// returned by 'ec2.DescribeInstanceTypes'.
package filter

import (
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// For more reading on the related 'ec2' / 'ec2/types' constructs involved here,
// see below.
//
// 'DescribeInstanceTypesOutput' is the type returned by
var _ ec2.DescribeInstanceTypesOutput
