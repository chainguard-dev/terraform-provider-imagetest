package ec2

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/google/go-containerregistry/pkg/name"
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

	// PrePullImages are image references the driver pulls into the
	// instance's Docker daemon during Setup, before any test container
	// starts. The pull uses the caller's registry credentials, sent to the
	// daemon per request, so the instance never stores them. Existing
	// instance mode only.
	PrePullImages []string

	// Commands run on the instance on failure, see Diagnose.
	OnFailure []string

	// Operational
	SkipTeardown bool
	Timeouts     drivers.Timeouts

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
		for _, ref := range c.PrePullImages {
			if _, err := name.ParseReference(ref); err != nil {
				return fmt.Errorf("pre-pull image %q: %w", ref, err)
			}
		}
		return nil
	}

	if len(c.PrePullImages) > 0 {
		return fmt.Errorf("pre-pull images are only supported with existing_instance")
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
