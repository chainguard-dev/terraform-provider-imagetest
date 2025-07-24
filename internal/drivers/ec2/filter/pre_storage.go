package filter

import "github.com/aws/aws-sdk-go-v2/service/ec2/types"

type filtersStoragePre struct{}

// The storage technology for the local instance storage disks.
//
// Values:
// | hdd
// | ssd
func (*filtersStoragePre) DiskType(typ types.DiskType) types.Filter {
	const name = "instance-storage-info.disk.type"
	return newFilterPre(name, string(typ))
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
		return newFilterPre(name, string(types.EphemeralNvmeSupportRequired))
	}
	return newFilterPre(name, string(types.EphemeralNvmeSupportUnsupported))
}

// Indicates whether data is encrypted at rest.
//
// Values:
// | required
// | supported
// | unsupported
func (*filtersStoragePre) SupportsEncryptionAtRest(state types.InstanceStorageEncryptionSupport) types.Filter {
	const name = "instance-storage-info.encryption-support"
	return newFilterPre(name, string(state))
}

// Indicates whether the instance type has local instance storage.
//
// Values:
// | true
// | false
func (*filtersStoragePre) Supported(supported bool) types.Filter {
	const name = "instance-storage-supported"
	if supported {
		return newFilterPre(name, "true")
	}
	return newFilterPre(name, "false")
}

// The root device type (ebs | instance-store).
func (*filtersStoragePre) RootDeviceType(typ types.RootDeviceType) types.Filter {
	const name = "supported-root-device-type"
	return newFilterPre(name, string(typ))
}
