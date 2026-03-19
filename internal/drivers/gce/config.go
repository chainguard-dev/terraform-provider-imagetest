package gce

import "fmt"

// Config configures the GCE driver.
type Config struct {
	// Required (unless ExistingInstance is set)
	ProjectID string
	Zone      string // e.g., "us-west1-b" (zone-level for GPU availability)
	Network   string // VPC network name or self-link

	// Optional with defaults
	Image          string // e.g., "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts"
	MachineType    string // default: "n1-standard-4"
	RootDiskSizeGB int64  // default: 50
	RootDiskType   string // default: "pd-ssd"
	SSHUser        string // default: "ubuntu"
	SSHPort        int32  // default: 22
	Shell          string // default: "bash"

	// GPU (GCE-specific: attached separately from machine type)
	AcceleratorType  string // e.g., "nvidia-tesla-t4", "nvidia-l4"
	AcceleratorCount int32  // default: 0

	// IAM (optional — defaults to default compute SA)
	ServiceAccountEmail string

	// Test execution
	SetupCommands []string
	Env           map[string]string
	StartupScript string // GCE metadata "startup-script"

	// Container (same as EC2)
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
	if c.MachineType == "" {
		c.MachineType = "n1-standard-4"
	}
	if c.RootDiskSizeGB == 0 {
		c.RootDiskSizeGB = 50
	}
	if c.RootDiskType == "" {
		c.RootDiskType = "pd-ssd"
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

	if c.ProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	if c.Zone == "" {
		return fmt.Errorf("zone is required")
	}
	if c.Network == "" {
		return fmt.Errorf("network is required")
	}
	if c.Image == "" {
		return fmt.Errorf("image is required")
	}
	if c.AcceleratorCount > 0 && c.AcceleratorType == "" {
		return fmt.Errorf("accelerator_type is required when accelerator_count > 0")
	}
	return nil
}
