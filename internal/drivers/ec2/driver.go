package ec2

import (
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
)

var _ drivers.Tester = (*Driver)(nil)

type Driver struct {
	/////////////////////////////////////////////////////////////////////////////
	// Public API

	// The AMI to launch the instance with
	AMI string

	// The instance architecture
	Arch types.ArchitectureType

	// The desired instance type (ex: `t3.medium`).
	//
	// NOTE: If provided, this input supersedes all other configuration (VCPUs,
	// memory, GPUs, etc.)!
	InstanceType types.InstanceType

	// Whether the instance type is eligible for free tier use
	FreeTierEligible bool

	// Instance virtual processor configuration
	Proc Proc

	// Instance physical memory configuration
	Memory Memory

	// Instance storage configuration
	Disks []Disk

	// Instance accelerator configuration
	GPU GPU

	/////////////////////////////////////////////////////////////////////////////
	// Unexported Fields

	// client holds a configured ec2 client for use in the `Setup` and `Teardown`
	// phases.
	client *ec2.Client

	// instanceID holds the launched instance ID for teardown later
	instanceID *string

	// instanceAddr holds the public IP address of the launched instance
	instanceAddr *string

	// stack is a LIFO queue of resources we create which will be destroyed in
	// the reverse order when finished
	stack ResourceStack
}

func (self *Driver) SetClient(client *ec2.Client) {
	self.client = client
}

var DriverDefault = &Driver{
	AMI:          "TODO",
	Arch:         types.ArchitectureTypeX8664,
	InstanceType: types.InstanceTypeT3Medium,
}

///////////////////////////////////////////////////////////////////////////////
// General Instance Filters

type filtersGeneralPre struct{}

// The virtualization type.
//
// Values:
// | hvm
// | paravirtual
func (*filtersGeneralPre) SupportedVirtualizationType(typ types.VirtualizationType) types.Filter {
	const name = "supported-virtualization-type"
	return filter(name, string(typ))
}

// The usage class.
//
// Values:
// | on-demand
// | spot
// | capacity-block
func (*filtersGeneralPre) SupportedUsageClass(class types.UsageClassType) types.Filter {
	const name = "supported-usage-class"
	return filter(name, string(class))
}

// The boot mode
//
// Values:
// | legacy-bios
// | uefi
func (*filtersGeneralPre) BootMode(mode types.BootModeType) types.Filter {
	const name = "supported-boot-mode"
	return filter(name, string(mode))
}

// The hypervisor family.
//
// Values:
// | nitro
// | xen
func (*filtersGeneralPre) HypervisorKind(kind types.HypervisorType) types.Filter {
	const name = "hypervisor"
	return filter(name, string(kind))
}

// Indicates whether the instance type is eligible to use in the free tier.
//
// Values:
// | true
// | false
func (*filtersGeneralPre) IsFreeTierEligible(is bool) types.Filter {
	const name = "free-tier-eligible"
	if is {
		return filter(name, "true")
	}
	return filter(name, "false")
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
		return filter(name, "true")
	}
	return filter(name, "false")
}
