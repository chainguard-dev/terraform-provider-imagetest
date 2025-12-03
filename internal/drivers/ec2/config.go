package ec2

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Config configures the EC2 driver.
type Config struct {
	// Required (unless ExistingInstance is set)
	VPCID string
	AMI   string

	// Optional with defaults
	Region         string // default: us-west-2
	InstanceType   string // default: t3.medium
	RootVolumeSize int32  // default: 50 (GB)
	SSHUser        string // default: ubuntu
	SSHPort        int32  // default: 22
	Shell          string // default: bash

	// Optional - created/detected if empty
	InstanceProfileName string
	SubnetCIDR          string // default: auto-find available /24 in VPC

	// Test execution
	SetupCommands []string
	Env           map[string]string
	UserData      string

	// Container
	VolumeMounts []string
	DeviceMounts []string
	GPUs         string // "all", "0", "1", "2", etc. Empty means no GPUs.

	// Operational
	SkipTeardown bool

	// Use existing instance (skips resource creation)
	ExistingInstance *ExistingInstance
}

// ExistingInstance configures the driver to use a pre-existing instance.
type ExistingInstance struct {
	IP     string // required - instance IP address
	SSHKey string // required - path to private key file
}

func (c *Config) applyDefaults() {
	if c.Region == "" {
		c.Region = "us-west-2"
	}
	if c.InstanceType == "" {
		c.InstanceType = "t3.medium"
	}
	if c.RootVolumeSize == 0 {
		c.RootVolumeSize = 50
	}
	if c.SSHUser == "" {
		c.SSHUser = "ubuntu"
	}
	if c.SSHPort == 0 {
		c.SSHPort = 22
	}
	if c.Shell == "" {
		c.Shell = "bash"
	}
	if c.Env == nil {
		c.Env = make(map[string]string)
	}
}

func (c *Config) validate() error {
	if c.ExistingInstance != nil {
		if c.ExistingInstance.IP == "" {
			return fmt.Errorf("existing_instance.ip is required")
		}
		if c.ExistingInstance.SSHKey == "" {
			return fmt.Errorf("existing_instance.ssh_key is required")
		}
		return nil
	}

	if c.VPCID == "" {
		return fmt.Errorf("vpc_id is required")
	}
	if c.AMI == "" {
		return fmt.Errorf("ami is required")
	}
	return nil
}

func (c *Config) instanceType() types.InstanceType {
	return types.InstanceType(c.InstanceType)
}
