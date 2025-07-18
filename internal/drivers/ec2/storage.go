package ec2

import (
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
