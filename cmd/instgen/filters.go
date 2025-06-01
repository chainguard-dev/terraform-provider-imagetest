package main

import (
	"iter"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/cmd/instgen/filter"
)

type (
	InstanceIter = iter.Seq[*types.InstanceTypeInfo]
	Filter       = filter.Filter[*types.InstanceTypeInfo]
)

// Any filter added to this array will be logically ANDed
var instanceTypeFilters = []Filter{
	// typeIsInFamily,
}

// func typeIsInFamily(t *types.InstanceTypeInfo) bool {
// 	// Instance types are distinct string types defined like `[family].[kind]`.
// 	// We're after . . . . . . . . . . . . . . . . . . . . .  ^------^

// 	// Stringify the instance type
// 	instanceTypeStr := string(t.InstanceType)

// 	// Get the first '.' index
// 	i := strings.IndexByte(instanceTypeStr, '.')
// 	if i < 0 { // Bounds check
// 		return false
// 	} else if i >= len(instanceTypeStr) { // Bounds check
// 		return false
// 	}

// 	// Slice out the instance family
// 	family := instanceTypeStr[:i]

// 	// Look for a match in our instance family map keys
// 	for k := range instFamilyMap {
// 		if k == family {
// 			return true
// 		}
// 	}

// 	return false
// }
