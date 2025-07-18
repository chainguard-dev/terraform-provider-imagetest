package ec2

import (
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type Proc struct {
	// The processor architecture
	Architecture types.ArchitectureType

	// The number of logical processors
	VCPUs uint16
}
