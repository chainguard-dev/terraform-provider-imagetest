package ec2

// instance_type_filters.go provides a functional api for assembling a slice of
// AWS EC2 'types.Filter', primarily used to filter and select an EC2 instance
// type during 'Driver.Setup()'.
//
// See nested package 'filter's 'doc.go' for more details.

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2/filter"
)

////////////////////////////////////////////////////////////////////////////////
// Pre Filters

// filtersPreBuild assembles a slice of AWS SDK 'types.Filter' for selection
// of an EC2 instance type. These filters are assembled using the current state
// of the 'Driver' instance provided.
//
// See nested package 'filter's 'doc.go' for more details.
func filtersPreBuild(ctx context.Context, d *Driver) ([]types.Filter, error) {
	filters := make([]types.Filter, 0, 24) // TODO: Get the max filter count.

	filters = filtersPreGeneral(ctx, d, filters)
	filters = filtersPreStorage(ctx, d, filters)
	filters = filtersPreProc(ctx, d, filters)
	filters = filtersPreMemory(ctx, d, filters)
	// NOTE: GPU filtering must be done on the results, it cannot be filtered in
	// the request.

	return filters, nil
}

func filtersPreGeneral(ctx context.Context, d *Driver, filters []types.Filter) []types.Filter {
	log := clog.FromContext(ctx)
	if d.FreeTierEligible {
		log.Debug("appending filter", "is_free_tier_eligible", true)
		filters = append(filters, filter.Pre.General.IsFreeTierEligible(true))
	}
	return filters
}

// filtersPreStorage appends to provided input slice `filters` any filters
// implied by values assigned to the `Disk` instances nested in `Driver`.
func filtersPreStorage(ctx context.Context, d *Driver, filters []types.Filter) []types.Filter {
	log := clog.FromContext(ctx)
	if len(d.Disks) == 0 {
		log.Debug("no storage constraints defined, returning early")
		return filters
	}
	// Indicate we require instance storage.
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

// filtersPreProc appends to provided input slice `filters` any filters
// implied by values assigned to the `Proc` instance nested in `Driver`.
func filtersPreProc(ctx context.Context, d *Driver, filters []types.Filter) []types.Filter {
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

// filtersPreMemory appends to provided input slice `filters` any filters
// implied by values assigned to the `Memory` instance nested in `Driver`.
func filtersPreMemory(ctx context.Context, d *Driver, filters []types.Filter) []types.Filter {
	log := clog.FromContext(ctx)

	if d.Memory.Capacity != "" {
		// Parse memory capacity input
		mem := parseMemoryCapacity(ctx, d.Memory.Capacity)
		log.Debug("appending filter", "memory_capacity_mib", mem)
		filters = append(filters, filter.Pre.Memory.Capacity(mem))
	}

	return filters
}

////////////////////////////////////////////////////////////////////////////////
// Post Filters

// filtersPostApply filters a provided slice of EC2 instance types, as returned
// by 'service/ec2.Client.DescribeInstanceTypes', where the filter criteria is
// determined from the state of the provided 'Driver' instance. The returned
// slice will contain only those which satisfy all criteria described by
// 'Driver'.
//
// See nested package 'filter's 'doc.go' for more details.
func filtersPostApply(ctx context.Context, d *Driver, instanceTypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	instanceTypes = filtersPostStorage(ctx, d, instanceTypes)
	instanceTypes = filtersPostGPU(ctx, d, instanceTypes)
	return instanceTypes
}

func filtersPostStorage(ctx context.Context, d *Driver, instanceTypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)
	if len(d.Disks) == 0 {
		log.Debug("skipping post disk filter (no disk constraints)")
		return instanceTypes
	}
	instanceTypes = filter.Post.Storage.DiskCount(ctx, uint8(len(d.Disks)), instanceTypes)
	diskCapacity := uint16(0)
	for _, disk := range d.Disks {
		if disk.Capacity > 0 {
			diskCapacity += disk.Capacity
		}
	}
	instanceTypes = filter.Post.Storage.DiskCapacity(ctx, diskCapacity, instanceTypes)
	return instanceTypes
}

func filtersPostGPU(ctx context.Context, d *Driver, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	if d.GPU.Count == 0 {
		return itypes
	}
	itypes = filter.Post.GPU.Count(ctx, d.GPU.Count, itypes)
	itypes = filter.Post.GPU.Kind(ctx, d.GPU.Kind, itypes)
	return itypes
}
