package ec2

import (
	"context"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

type Proc struct {
	// The processor architecture
	Architecture types.ArchitectureType

	// The number of logical processors
	VCPUs uint16
}

// buildProcFilters appends to provided input slice `filters` any filters
// implied by values assigned to the `Proc` instance nested in `Driver`.
func buildProcFilters(ctx context.Context, d *Driver, filters []types.Filter) []types.Filter {
	log := clog.FromContext(ctx)

	if d.Proc.Architecture != "" {
		logFilterAdd(log, "processor architecture", d.Proc.Architecture)
		filters = append(filters, Pre.Proc.Architecture(d.Proc.Architecture))
	}

	if d.Proc.VCPUs > 0 {
		logFilterAdd(log, "vcpu cores", d.Proc.VCPUs)
		filters = append(filters, Pre.Proc.VCPUCores(d.Proc.VCPUs))
	}

	return filters
}

type filtersProcPre struct{}

// The processor architecture.
//
// Values:
// | arm64
// | i386
// | x86_64
func (*filtersProcPre) Architecture(kind types.ArchitectureType) types.Filter {
	const name = "processor-info.supported-architecture"
	return filter(name, string(kind))
}

// The number of cores that can be configured for the instance type.
func (*filtersProcPre) VCPUCores(count uint16) types.Filter {
	const name = "vcpu-info.valid-cores"
	return filter(name, strconv.Itoa(int(count)))
}
