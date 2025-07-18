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
}

func (self *Driver) SetClient(client *ec2.Client) {
	self.client = client
}

var DriverDefault = &Driver{
	AMI:          "TODO",
	Arch:         types.ArchitectureTypeX8664,
	InstanceType: types.InstanceTypeT3Medium,
}
