package ec2

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2/filter"
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
	// log := clog.FromContext(ctx)

	if d.Proc.Architecture != "" {
		// logFilterAdd(log, "processor architecture", d.Proc.Architecture)
		filters = append(filters, filter.Pre.Proc.Architecture(d.Proc.Architecture))
	}

	if d.Proc.VCPUs > 0 {
		// logFilterAdd(log, "vcpu cores", d.Proc.VCPUs)
		filters = append(filters, filter.Pre.Proc.VCPUCores(d.Proc.VCPUs))
	}

	return filters
}
