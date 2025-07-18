package ec2

// instance_type_filters.go provides a functional interface for assembling a
// list of AWS EC2 filters, primarily used to filter and select an EC2 instance
// type during `Driver.Setup()`.

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2/filter"
)

func buildPreFilters(ctx context.Context, d *Driver) ([]types.Filter, error) {
	filters := make([]types.Filter, 0, 24) // TODO: Get the max filter count

	filters = buildGeneralFilters(ctx, d, filters)
	filters = buildStorageFiltersPre(ctx, d, filters)
	filters = buildProcFilters(ctx, d, filters)
	filters = buildMemoryFilters(ctx, d, filters)
	// NOTE: GPU filtering must be done on the results, it cannot be filtered in
	// the request

	return filters, nil
}

////////////////////////////////////////////////////////////////////////////////
// Pre Filters

func buildGeneralFilters(ctx context.Context, d *Driver, filters []types.Filter) []types.Filter {
	log := clog.FromContext(ctx)

	if d.FreeTierEligible {
		log.Debug("appending filter", "is_free_tier_eligible", true)
		filters = append(filters, filter.Pre.General.IsFreeTierEligible(true))
	}

	return filters
}

////////////////////////////////////////////////////////////////////////////////
// Post Filters

func applyPostFilters(ctx context.Context, d *Driver, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	itypes = applyFiltersStoragePost(ctx, d.Disks, itypes)
	itypes = applyGPUFiltersPost(ctx, d, itypes)
	return itypes
}

func applyFiltersStoragePost(ctx context.Context, disks []Disk, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)

	if len(disks) == 0 {
		log.Debug("skipping post disk filter (no disk constraints)")
		return itypes
	}

	itypes = filter.Post.Storage.DiskCount(ctx, uint8(len(disks)), itypes)
	diskCapacity := uint16(0)
	for _, disk := range disks {
		if disk.Capacity > 0 {
			diskCapacity += disk.Capacity
		}
	}
	itypes = filter.Post.Storage.DiskCapacity(ctx, diskCapacity, itypes)

	return itypes
}

func applyGPUFiltersPost(ctx context.Context, d *Driver, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	if d.GPU.Count == 0 {
		return itypes
	}

	itypes = filter.Post.GPU.Count(ctx, d.GPU.Count, itypes)
	itypes = filter.Post.GPU.Kind(ctx, d.GPU.Kind, itypes)

	return itypes
}
