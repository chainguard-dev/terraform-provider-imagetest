package filter

import (
	"strconv"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type filtersMemoryPre struct{}

// The memory size (in MB).
func (*filtersMemoryPre) Capacity(sz uint32) types.Filter {
	const name = "memory-info.size-in-mib"
	return newFilterPre(name, strconv.Itoa(int(sz)))
}
