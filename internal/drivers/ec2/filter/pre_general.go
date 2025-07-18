package filter

import "github.com/aws/aws-sdk-go-v2/service/ec2/types"

type filtersGeneralPre struct{}

// The virtualization type.
//
// Values:
// | hvm
// | paravirtual
func (*filtersGeneralPre) SupportedVirtualizationType(typ types.VirtualizationType) types.Filter {
	const name = "supported-virtualization-type"
	return newFilterPre(name, string(typ))
}

// The usage class.
//
// Values:
// | on-demand
// | spot
// | capacity-block
func (*filtersGeneralPre) SupportedUsageClass(class types.UsageClassType) types.Filter {
	const name = "supported-usage-class"
	return newFilterPre(name, string(class))
}

// The boot mode
//
// Values:
// | legacy-bios
// | uefi
func (*filtersGeneralPre) BootMode(mode types.BootModeType) types.Filter {
	const name = "supported-boot-mode"
	return newFilterPre(name, string(mode))
}

// The hypervisor family.
//
// Values:
// | nitro
// | xen
func (*filtersGeneralPre) HypervisorKind(kind types.HypervisorType) types.Filter {
	const name = "hypervisor"
	return newFilterPre(name, string(kind))
}

// Indicates whether the instance type is eligible to use in the free tier.
//
// Values:
// | true
// | false
func (*filtersGeneralPre) IsFreeTierEligible(is bool) types.Filter {
	const name = "free-tier-eligible"
	if is {
		return newFilterPre(name, "true")
	}
	return newFilterPre(name, "false")
}

// Indicates whether this instance type is the latest generation instance type
// of an instance family.
//
// Values:
// | true
// | false
func (*filtersGeneralPre) IsCurrentGeneration(yes bool) types.Filter {
	const name = "current-generation"
	if yes {
		return newFilterPre(name, "true")
	}
	return newFilterPre(name, "false")
}
