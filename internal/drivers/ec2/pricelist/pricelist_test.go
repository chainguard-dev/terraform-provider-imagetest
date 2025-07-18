package pricelist

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
)

func TestCheapest(t *testing.T) {
	// Standard case
	x, pc := SelectCheapest([]types.InstanceType{
		"c6in.8xlarge",  // 1.8144
		"r5ad.4xlarge",  // 1.0480
		"r5d.4xlarge",   // 1.1520
		"r5.4xlarge",    // 1.0080
		"c7g.metal",     // 2.3200
		"c6gn.12xlarge", // 2.0736
		"r6i.32xlarge",  // 8.0640
		"cr1.8xlarge",   // 3.5000
		"r7a.24xlarge",  // 7.3032
		"p4d.24xlarge",  // 21.9576
		"c7gn.16xlarge", // 3.9936
	})
	assert.Equal(t, types.InstanceTypeR54xlarge, x)
	assert.Equal(t, float32(1.0080), pc)

	// Invalid instance type included
	x, pc = SelectCheapest([]types.InstanceType{
		"c6in.8xlarge",         // 1.8144
		"r5ad.4xlarge",         // 1.0480
		"r5d.4xlarge",          // 1.1520
		"r5.4xlarge",           // 1.0080
		"c7g.metal",            // 2.3200
		"c6gn.12xlarge",        // 2.0736
		"r6i.32xlarge",         // 8.0640
		"cr1.8xlarge",          // 3.5000
		"r7a.24xlarge",         // 7.3032
		"p4d.24xlarge",         // 21.9576
		"c7gn.16xlarge",        // 3.9936
		"notaninstance.xlarge", // Nope
	})
	assert.Equal(t, types.InstanceTypeR54xlarge, x)
	assert.Equal(t, float32(1.0080), pc)

	// Nil input
	x, pc = SelectCheapest(nil)
	assert.Equal(t, types.InstanceType(""), x)
	assert.Equal(t, float32(pc), pc)

	// Non-nil, zero-length input
	x, pc = SelectCheapest([]types.InstanceType{})
	assert.Equal(t, types.InstanceType(""), x)
	assert.Equal(t, float32(0), pc)
}
