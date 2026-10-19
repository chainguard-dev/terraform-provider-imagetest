// Package sshhost runs imagetest containers on a Docker host reached over
// SSH. It is the part of a driver that starts once a host exists: the ec2
// driver uses it after launching an instance, the ssh driver for a host it
// is given.
package sshhost

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	issh "github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/kballard/go-shellquote"
	"go.opentelemetry.io/otel"
	"golang.org/x/crypto/ssh"
)

// Host is a Docker host reached over SSH. Every connection, the Docker
// tunnel and the Go client alike, authenticates with the same key and, when
// HostKeys is set, accepts only those host keys. Set the paths, call Load,
// then use it.
type Host struct {
	// Name prefixes the test containers' names, e.g. "imagetest-ec2-1a2b3c4d".
	Name string
	// Driver names the driver in trace spans, e.g. "ec2".
	Driver string

	Addr string
	Port int32
	User string

	// KeyPath is the private key file. OpenSSH reads it for the Docker
	// tunnel; Signer is the same key parsed for the Go client.
	KeyPath string
	Signer  ssh.Signer

	// KnownHosts is an OpenSSH known_hosts file the Docker tunnel pins the
	// host key with, and HostKeys are the keys it holds for Addr, for the Go
	// client. Both empty means host keys are not checked.
	KnownHosts string
	HostKeys   []ssh.PublicKey

	// Shell runs SetupCommands and OnFailure, with Env exported. Env is
	// also set in the test container.
	Shell         string
	Env           map[string]string
	SetupCommands []string
	OnFailure     []string

	// Container settings. GPUs is "all", a count, or "" for none.
	GPUs         string
	VolumeMounts []string
	DeviceMounts []string

	// PullECRWithHostCredentials pulls images from private ECR registries
	// with the docker CLI on the host, through docker-credential-ecr-login
	// and the host's own AWS credentials, instead of sending the caller's
	// registry credentials to the daemon. The host needs the helper on its
	// PATH and credentials that can read ECR, such as an EC2 instance
	// profile. Pulls fail without them.
	PullECRWithHostCredentials bool

	// Stack receives each test container's removal.
	Stack *harness.Stack
}

// Load fills Signer from KeyPath and HostKeys from KnownHosts, so the Go
// client and the OpenSSH tunnel verify the same key and the same pin. It is
// an error for KnownHosts to hold no key for Addr.
func (h *Host) Load() error {
	data, err := os.ReadFile(h.KeyPath)
	if err != nil {
		return fmt.Errorf("reading SSH key %s: %w", h.KeyPath, err)
	}
	h.Signer, err = issh.ParseKey(data, nil)
	if err != nil {
		return fmt.Errorf("parsing SSH key: %w", err)
	}
	if h.KnownHosts != "" {
		h.HostKeys, err = LoadKnownHosts(h.KnownHosts, h.Addr, h.Port)
		if err != nil {
			return fmt.Errorf("loading known_hosts: %w", err)
		}
	}
	return nil
}

// Connect opens an SSH connection to the host. With pinned host keys the
// connection fails unless the host presents one of them. The dial and the
// handshake end when ctx does; sessions on the connection do not, see
// connect.
func (h *Host) Connect(ctx context.Context) (*ssh.Client, error) {
	return issh.ConnectContext(ctx, h.Addr, uint16(h.Port), h.User, h.Signer, h.HostKeys...)
}

// connect is Connect with the established connection also bound to ctx: when
// ctx ends the connection is closed, which unblocks a running session, whose
// Wait returns only after its output is copied. The returned func releases
// the binding and closes the connection.
func (h *Host) connect(ctx context.Context) (*ssh.Client, func(), error) {
	conn, err := h.Connect(ctx)
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return conn, func() {
		stop()
		_ = conn.Close()
	}, nil
}

// Labels returns the labels for a test container, the same keys the ec2
// driver tags its AWS resources with, so a host operator can find and prune
// what a run left behind: docker container prune --filter label=imagetest.
func (h *Host) Labels() map[string]string {
	return map[string]string{
		"imagetest":           "true",
		"imagetest:driver":    h.Driver,
		"imagetest:test-name": h.Name,
		"imagetest:expires":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
	}
}

// Run pulls ref into the host's Docker daemon (see pullImage) and runs it as
// a test container.
func (h *Host) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	log := clog.FromContext(ctx)

	cli, err := h.dockerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	defer cli.Close()

	if err := h.waitDocker(ctx, cli); err != nil {
		return nil, err
	}

	log.Info("pulling image", "image", ref.String())
	if err := h.pullImage(ctx, cli, ref); err != nil {
		return nil, fmt.Errorf("pulling image: %w", err)
	}

	log.Info("running container", "image", ref.String())
	return h.runContainer(ctx, cli, ref)
}

// CollectGPUInfo connects via SSH and logs GPU and NVIDIA driver information
// if nvidia-smi is available on the host. This is non-fatal; failures are
// logged as warnings.
func (h *Host) CollectGPUInfo(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ctx, span := otel.Tracer("imagetest").Start(ctx, h.Driver+".gpu_info")
	defer span.End()

	log := clog.FromContext(ctx)

	conn, done, err := h.connect(ctx)
	if err != nil {
		log.Warn("SSH connection failed for GPU info", "error", err)
		return
	}
	defer done()

	// Check if nvidia-smi is available
	checkBuf := new(bytes.Buffer)
	if err := issh.ExecIn(conn, issh.ShellBash, checkBuf, new(bytes.Buffer), "command -v nvidia-smi"); err != nil {
		log.Info("nvidia-smi not found, skipping GPU info collection")
		return
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	// Query all useful GPU properties in a single nvidia-smi call
	cmds := []string{
		"nvidia-smi --query-gpu=name,driver_version,vbios_version,pci.bus_id,memory.total,memory.free,compute_cap,gpu_serial,uuid --format=csv",
		"echo '=== nvidia kernel module ==='",
		"cat /sys/module/nvidia/version 2>/dev/null || echo 'nvidia kernel module not loaded'",
		"echo '=== cuda version ==='",
		"nvidia-smi --query-gpu=driver_version --format=csv,noheader | head -1",
		"nvcc --version 2>/dev/null || echo 'nvcc not installed'",
	}

	if err := issh.ExecIn(conn, issh.ShellBash, stdout, stderr, cmds...); err != nil {
		log.Warn("failed to collect GPU info", "error", err, "stderr", stderr.String())
		return
	}

	log.Info("GPU info", "output", stdout.String())
}

// RunSetupCommands runs SetupCommands on the host in one SSH session, after
// the standard shell options and the Env exports.
func (h *Host) RunSetupCommands(ctx context.Context) error {
	if len(h.SetupCommands) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)

	conn, done, err := h.connect(ctx)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer done()

	cmds := append([]string{cmdStdOpts}, envExports(h.Env)...)
	cmds = append(cmds, h.SetupCommands...)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	log.Info("running setup commands", "count", len(h.SetupCommands))
	if err := issh.ExecIn(conn, h.Shell, stdout, stderr, cmds...); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("setup commands: %w", cerr)
		}
		log.Error("setup commands failed", "stdout", stdout.String(), "stderr", stderr.String())
		return fmt.Errorf(
			"setup commands failed: %w\n--- stdout (last %d lines) ---\n%s\n--- stderr (last %d lines) ---\n%s",
			err,
			setupFailureTailLines, tailLines(stdout.String(), setupFailureTailLines),
			setupFailureTailLines, tailLines(stderr.String(), setupFailureTailLines),
		)
	}

	log.Info("setup commands complete")
	return nil
}

// envExports returns `export` lines for env, sorted by name so the order is
// stable. Values are quoted, so no export depends on another.
func envExports(env map[string]string) []string {
	keys := slices.Sorted(maps.Keys(env))
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("export %s=%s", k, shellquote.Join(env[k])))
	}
	return lines
}

// setupFailureTailLines is the number of trailing lines of stdout/stderr
// included in the error returned by RunSetupCommands. Keeps long-running
// setup_commands' captured output bounded while still showing the failing
// context.
const setupFailureTailLines = 200

// tailLines returns the last n lines of s (newline-separated). If s has
// at most n lines, it's returned unchanged. The truncation marker reports
// the original count so consumers know how much was dropped.
func tailLines(s string, n int) string {
	if s == "" {
		return "(empty)"
	}
	trimmed := strings.TrimRight(s, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= n {
		return trimmed
	}
	return fmt.Sprintf("(...truncated to last %d of %d lines...)\n%s",
		n, len(lines),
		strings.Join(lines[len(lines)-n:], "\n"),
	)
}
