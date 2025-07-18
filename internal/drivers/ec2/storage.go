package ec2

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2/filter"
)

// Disk describes an EC2 instance volume.
type Disk struct {
	// Type of disk (ex: SSD, NVME, tape)
	Kind types.DiskType

	// Disk capacity (in gigabytes)
	Capacity uint16

	// Whether the disk supports NVME
	NVMESupport bool
}

// buildStorageFiltersPre appends to provided input slice `filters` any filters
// implied by values assigned to the `Disk` instances nested in `Driver`.
func buildStorageFiltersPre(ctx context.Context, d *Driver, filters []types.Filter) []types.Filter {
	log := clog.FromContext(ctx)

	if len(d.Disks) == 0 {
		log.Debug("no storage constraints defined, returning early")
		return filters
	}

	// Indicate we require instance storage
	// logFilterAdd(log, "storage supported", true)
	filters = append(filters, filter.Pre.Storage.Supported(true))

	// Set default capacity and nvme requirement
	//
	// Since we may have to enumerate >1 disk, we init these with zero-values
	// then add any necessary changes. Further down, if these are still at their
	// defaults, we simply don't add the filter.
	diskCap := 0
	nvmeSupport := false
	for _, disk := range d.Disks {
		if disk.Capacity > 0 {
			diskCap += int(disk.Capacity)
		}
		if disk.NVMESupport {
			nvmeSupport = true
		}
	}

	// Indicate required NVME support
	if nvmeSupport {
		// logFilterAdd(log, "storage nvme support", true)
		filters = append(filters, filter.Pre.Storage.SupportsNVME(true))
	}

	return filters
}
