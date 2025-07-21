package pricelist

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
)

func TestCheapest(t *testing.T) {
	// This package is a generated price list that changes pretty frequently. To
	// ensure some consistency with our tests, we've got to game the system a bit
	// by manually assigning a 'cheapest' instance type.
	const cheapestPrice float32 = 0.1
	const cheapestInstanceType = types.InstanceTypeR54xlarge
	priceList[cheapestInstanceType] = cheapestPrice
	// Standard case
	x, pc := SelectCheapest([]types.InstanceType{
		cheapestInstanceType,
		"c6in.8xlarge",
		"r5ad.4xlarge",
		"r5d.4xlarge",
		"c7g.metal",
		"c6gn.12xlarge",
		"r6i.32xlarge",
		"cr1.8xlarge",
		"r7a.24xlarge",
		"p4d.24xlarge",
		"c7gn.16xlarge",
	})
	assert.Equal(t, cheapestInstanceType, x)
	assert.Equal(t, cheapestPrice, pc)

	// Invalid instance type included
	x, pc = SelectCheapest([]types.InstanceType{
		cheapestInstanceType,
		"c6in.8xlarge",
		"r5ad.4xlarge",
		"r5d.4xlarge",
		"c7g.metal",
		"c6gn.12xlarge",
		"r6i.32xlarge",
		"cr1.8xlarge",
		"r7a.24xlarge",
		"p4d.24xlarge",
		"c7gn.16xlarge",
		"notaninstance.xlarge",
	})
	assert.Equal(t, cheapestInstanceType, x)
	assert.Equal(t, cheapestPrice, pc)

	// Nil input
	x, pc = SelectCheapest(nil)
	assert.Equal(t, types.InstanceType(""), x)
	assert.Equal(t, float32(pc), pc)

	// Non-nil, zero-length input
	x, pc = SelectCheapest([]types.InstanceType{})
	assert.Equal(t, types.InstanceType(""), x)
	assert.Equal(t, float32(0), pc)
}
