package ssh

import (
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/google/go-containerregistry/pkg/name"
)

// Config configures the ssh driver.
type Config struct {
	// Required
	Host   string // address of the host: hostname, IPv4 or IPv6
	SSHKey string // path to the private key file
	// KnownHosts is the path to an OpenSSH known_hosts file holding the
	// host's key. Every SSH connection verifies the host against it and
	// fails on a mismatch.
	KnownHosts string

	// Optional with defaults
	Port  int32  // default: 22
	User  string // default: ubuntu
	Shell string // default: bash

	// Host-side execution
	SetupCommands []string
	Env           map[string]string

	// Container
	VolumeMounts []string
	DeviceMounts []string
	GPUs         string // "all", "0", "1", "2", etc. Empty means no GPUs.

	// PrePullImages are image references the driver pulls into the host's
	// Docker daemon during Setup, before any test container starts, with the
	// caller's registry credentials.
	PrePullImages []string

	// Commands run on the host on failure, see Diagnose.
	OnFailure []string

	// Operational
	SkipTeardown bool
	Timeouts     drivers.Timeouts
}

func (c *Config) applyDefaults() {
	if c.Port == 0 {
		c.Port = 22
	}
	if c.User == "" {
		c.User = "ubuntu"
	}
	if c.Shell == "" {
		c.Shell = "bash"
	}
	if c.Env == nil {
		c.Env = make(map[string]string)
	}
}

func (c *Config) validate() error {
	if c.Host == "" {
		return fmt.Errorf("host is required")
	}
	if c.SSHKey == "" {
		return fmt.Errorf("ssh_key is required")
	}
	if c.KnownHosts == "" {
		return fmt.Errorf("known_hosts is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d is out of range", c.Port)
	}
	for _, ref := range c.PrePullImages {
		if _, err := name.ParseReference(ref); err != nil {
			return fmt.Errorf("pre-pull image %q: %w", ref, err)
		}
	}
	return nil
}
