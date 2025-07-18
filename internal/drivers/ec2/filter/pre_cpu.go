package filter

import (
	"strconv"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type filtersProcPre struct{}

// The processor architecture.
//
// Values:
// | arm64
// | i386
// | x86_64
func (*filtersProcPre) Architecture(kind types.ArchitectureType) types.Filter {
	const name = "processor-info.supported-architecture"
	return newFilterPre(name, string(kind))
}

// The number of cores that can be configured for the instance type.
func (*filtersProcPre) VCPUCores(count uint16) types.Filter {
	const name = "vcpu-info.valid-cores"
	return newFilterPre(name, strconv.Itoa(int(count)))
}
