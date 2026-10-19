package ssh

import (
	"fmt"
	"strings"

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
	SSHPort int32  // default: 22
	SSHUser string // default: ubuntu
	Shell   string // default: bash

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

	// LockPath, when set, is a file on the host that Setup takes an
	// exclusive flock on, held until Teardown, so runs that share the host
	// run one at a time. The wait is bounded by the setup timeout.
	LockPath string

	// Operational
	SkipTeardown bool // IMAGETEST_SKIP_TEARDOWN: leave the test containers on the host
	Timeouts     drivers.Timeouts
}

func (c *Config) applyDefaults() {
	if c.SSHPort == 0 {
		c.SSHPort = 22
	}
	if c.SSHUser == "" {
		c.SSHUser = "ubuntu"
	}
	// An IPv6 address may be given in brackets, as in a URL. The driver
	// brackets it itself where needed.
	c.Host = strings.TrimSuffix(strings.TrimPrefix(c.Host, "["), "]")
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
	if c.SSHPort < 1 || c.SSHPort > 65535 {
		return fmt.Errorf("ssh_port %d is out of range", c.SSHPort)
	}
	for _, ref := range c.PrePullImages {
		if _, err := name.ParseReference(ref); err != nil {
			return fmt.Errorf("pre-pull image %q: %w", ref, err)
		}
	}
	return nil
}
