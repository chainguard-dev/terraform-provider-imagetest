package ec2

import (
	"context"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
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
	logFilterAdd(log, "storage supported", true)
	filters = append(filters, Pre.Storage.Supported(true))

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
		logFilterAdd(log, "storage nvme support", true)
		filters = append(filters, Pre.Storage.SupportsNVME(true))
	}

	return filters
}

///////////////////////////////////////////////////////////////////////////////
// Pre Filters

type filtersStoragePre struct{}

// The storage technology for the local instance storage disks (hdd | ssd).
func (*filtersStoragePre) DiskType(typ types.DiskType) types.Filter {
	const name = "instance-storage-info.disk.type"
	return filter(name, string(typ))
}

// Indicates whether non-volatile memory express (NVMe) is supported for
// instance store
//
// Values:
// | required
// | supported
// | unsupported
func (*filtersStoragePre) SupportsNVME(supported bool) types.Filter {
	const name = "instance-storage-info.nvme-support"
	if supported {
		return filter(name, string(types.EphemeralNvmeSupportRequired))
	}
	return filter(name, string(types.EphemeralNvmeSupportUnsupported))
}

// Indicates whether data is encrypted at rest.
//
// Values:
// | required
// | supported
// | unsupported
func (*filtersStoragePre) SupportsEncryptionAtRest(state types.InstanceStorageEncryptionSupport) types.Filter {
	const name = "instance-storage-info.encryption-support"
	return filter(name, string(state))
}

// Indicates whether the instance type has local instance storage.
//
// Values:
// | true
// | false
func (*filtersStoragePre) Supported(supported bool) types.Filter {
	const name = "instance-storage-supported"
	if supported {
		return filter(name, "true")
	}
	return filter(name, "false")
}

// The root device type (ebs | instance-store).
func (*filtersStoragePre) RootDeviceType(typ types.RootDeviceType) types.Filter {
	const name = "supported-root-device-type"
	return filter(name, string(typ))
}

///////////////////////////////////////////////////////////////////////////////
// Post Filters

func applyFiltersStoragePost(ctx context.Context, d *Driver, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)

	if len(d.Disks) == 0 {
		log.Debug("skipping post disk filter (no disk constraints)")
		return itypes
	}

	itypes = Post.Storage.DiskCount(ctx, uint8(len(d.Disks)), itypes)
	diskCapacity := uint16(0)
	for _, disk := range d.Disks {
		if disk.Capacity > 0 {
			diskCapacity += disk.Capacity
		}
	}
	itypes = Post.Storage.DiskCapacity(ctx, diskCapacity, itypes)

	return itypes
}

type filtersStoragePost struct{}

func (*filtersStoragePost) DiskCount(ctx context.Context, count uint8, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)

	if count == 0 {
		log.Debug("skipping disk count evaluation")
		return itypes
	}

	return slices.DeleteFunc(itypes, func(typ types.InstanceTypeInfo) bool {
		log := log.With("instance_type", typ.InstanceType)

		if typ.InstanceStorageInfo == nil {
			log.Debug("instance has no storage info")
			return true
		}

		log.Debug("evaluating instance disk count", "have", len(typ.InstanceStorageInfo.Disks), "want", count)
		return len(typ.InstanceStorageInfo.Disks) < int(count)
	})
}

func (*filtersStoragePost) DiskCapacity(ctx context.Context, capacity uint16, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)

	if capacity == 0 {
		log.Debug("skipping disk capacity evaluation")
		return itypes
	}

	return slices.DeleteFunc(itypes, func(typ types.InstanceTypeInfo) bool {
		log := log.With("instance_type", typ.InstanceType)
		if typ.InstanceStorageInfo == nil {
			log.Debug("instance has no storage info")
			return true
		}

		for _, disk := range typ.InstanceStorageInfo.Disks {
			log.Debug("evaluating disk capacity", "have", *disk.SizeInGB, "want", capacity)
			if *disk.SizeInGB >= int64(capacity) {
				return false
			}
		}

		return true
	})
}
