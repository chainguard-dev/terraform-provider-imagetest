package gce

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	issh "github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"github.com/kballard/go-shellquote"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Teardown is a function that cleans up a resource.
type Teardown func(context.Context) error

// resource represents a GCE resource that can be created and torn down.
type resource interface {
	create(ctx context.Context) (Teardown, error)
}

type driver struct {
	name  string
	cfg   Config
	stack *harness.Stack

	instances *compute.InstancesClient
	firewalls *compute.FirewallsClient

	sshKey   *sshKey
	firewall *firewallRule
	instance *instance

	existingSigner ssh.Signer
	existingIP     string
}

func NewDriver(cfg Config, instancesClient *compute.InstancesClient, firewallsClient *compute.FirewallsClient) (*driver, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &driver{
		cfg:       cfg,
		stack:     harness.NewStack(),
		instances: instancesClient,
		firewalls: firewallsClient,
	}, nil
}

func (d *driver) Setup(ctx context.Context) error {
	if d.cfg.ExistingInstance != nil {
		return d.setupExistingInstance(ctx)
	}
	return d.setupNewInstance(ctx)
}

func (d *driver) setupExistingInstance(ctx context.Context) error {
	log := clog.FromContext(ctx)

	d.name = "imagetest-gce-" + uuid.New().String()[:8]
	log.Info("using existing instance", "name", d.name, "ip", d.cfg.ExistingInstance.IP)

	keyData, err := os.ReadFile(d.cfg.ExistingInstance.SSHKey)
	if err != nil {
		return fmt.Errorf("reading SSH key %s: %w", d.cfg.ExistingInstance.SSHKey, err)
	}

	signer, err := issh.ParseKey(keyData, nil)
	if err != nil {
		return fmt.Errorf("parsing SSH key: %w", err)
	}
	d.existingSigner = signer
	d.existingIP = d.cfg.ExistingInstance.IP

	// Run setup commands if any
	if err := d.runSetupCommands(ctx); err != nil {
		return fmt.Errorf("running setup commands: %w", err)
	}

	log.Info("existing instance ready", "ip", d.existingIP)
	return nil
}

func (d *driver) create(ctx context.Context, r resource) error {
	teardown, err := r.create(ctx)
	if err != nil {
		return err
	}
	if err := d.stack.Add(teardown); err != nil {
		return fmt.Errorf("adding teardown to stack: %w", err)
	}
	return nil
}

func (d *driver) setupNewInstance(ctx context.Context) error {
	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	d.name = "imagetest-gce-" + uuid.New().String()[:8]
	log.Info("setting up GCE driver", "name", d.name, "project", d.cfg.ProjectID, "zone", d.cfg.Zone)

	d.sshKey = &sshKey{
		name:    d.name + "-key",
		sshUser: d.cfg.SSHUser,
	}
	if err := d.create(ctx, d.sshKey); err != nil {
		return fmt.Errorf("creating SSH key: %w", err)
	}

	sshMetadata, err := d.sshKey.metadataEntry()
	if err != nil {
		return fmt.Errorf("generating SSH metadata: %w", err)
	}

	firewallTag := sanitizeGCEName(d.name)
	d.firewall = &firewallRule{
		client:    d.firewalls,
		projectID: d.cfg.ProjectID,
		network:   d.cfg.Network,
		name:      sanitizeGCEName(d.name + "-fw"),
		sshPort:   d.cfg.SSHPort,
		tag:       firewallTag,
	}
	if err := d.create(ctx, d.firewall); err != nil {
		return fmt.Errorf("creating firewall rule: %w", err)
	}

	// Build instance metadata
	metadata := map[string]string{
		"ssh-keys": sshMetadata,
	}
	if d.cfg.StartupScript != "" {
		metadata["startup-script"] = d.cfg.StartupScript
	}

	d.instance = &instance{
		client:              d.instances,
		projectID:           d.cfg.ProjectID,
		zone:                d.cfg.Zone,
		name:                sanitizeGCEName(d.name),
		machineType:         d.cfg.MachineType,
		image:               d.cfg.Image,
		diskSizeGB:          d.cfg.RootDiskSizeGB,
		diskType:            d.cfg.RootDiskType,
		network:             d.cfg.Network,
		metadata:            metadata,
		tags:                []string{firewallTag},
		labels:              buildLabels(d.name),
		sshPort:             d.cfg.SSHPort,
		acceleratorType:     d.cfg.AcceleratorType,
		acceleratorCount:    d.cfg.AcceleratorCount,
		serviceAccountEmail: d.cfg.ServiceAccountEmail,
	}
	if err := d.create(ctx, d.instance); err != nil {
		return fmt.Errorf("creating instance: %w", err)
	}
	span.AddEvent("gce.instance.created")

	if err := d.instance.wait(ctx); err != nil {
		return fmt.Errorf("waiting for instance: %w", err)
	}
	span.AddEvent("gce.instance.running")

	if err := d.prepareInstance(ctx); err != nil {
		return fmt.Errorf("preparing instance: %w", err)
	}
	span.AddEvent("gce.cloudinit.complete")

	if err := d.runSetupCommands(ctx); err != nil {
		return fmt.Errorf("running setup commands: %w", err)
	}
	span.AddEvent("gce.setup.complete")

	log.Info("GCE driver setup complete", "instance", d.instance.name, "public_ip", d.instance.publicIP)

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set - resources will not be cleaned up",
			"instance", d.instance.name,
			"instance_ip", d.instance.publicIP,
			"ssh_key", d.sshKeyPath(),
			"ssh_user", d.cfg.SSHUser,
		)
		log.Infof("to connect: ssh -i %s %s@%s", d.sshKeyPath(), d.cfg.SSHUser, d.instance.publicIP)
	}

	return nil
}

func (d *driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)

	// Close GCP clients regardless of mode
	defer func() {
		if d.instances != nil {
			_ = d.instances.Close()
		}
		if d.firewalls != nil {
			_ = d.firewalls.Close()
		}
	}()

	if d.cfg.ExistingInstance != nil {
		log.Info("existing instance mode - nothing to teardown")
		return nil
	}

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set, skipping teardown",
			"instance", d.instance.name,
			"instance_ip", d.instance.publicIP,
			"ssh_key", d.sshKeyPath(),
			"ssh_user", d.cfg.SSHUser,
		)
		log.Infof("to connect: ssh -i %s %s@%s", d.sshKeyPath(), d.cfg.SSHUser, d.instance.publicIP)
		return nil
	}

	log.Info("starting teardown")
	return d.stack.Teardown(ctx)
}

func (d *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	log := clog.FromContext(ctx)

	cli, err := d.dockerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	defer cli.Close()

	// Verify Docker is accessible
	log.Info("verifying Docker connection")
	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("docker is not accessible on the instance (ensure Docker is installed and running via startup_script or setup_commands): %w", err)
	}

	log.Info("pulling image", "image", ref.String())
	if err := d.pullImage(ctx, cli, ref); err != nil {
		return nil, fmt.Errorf("pulling image: %w", err)
	}

	log.Info("running container", "image", ref.String())
	return d.runContainer(ctx, cli, ref)
}

// instanceIP returns the IP address to connect to.
func (d *driver) instanceIP() string {
	if d.cfg.ExistingInstance != nil {
		return d.existingIP
	}
	return d.instance.publicIP
}

// sshSigner returns the SSH signer for authentication.
func (d *driver) sshSigner() (ssh.Signer, error) {
	if d.cfg.ExistingInstance != nil {
		return d.existingSigner, nil
	}
	return d.sshKey.private.ToSSH()
}

// sshKeyPath returns the path to the SSH key file.
func (d *driver) sshKeyPath() string {
	if d.cfg.ExistingInstance != nil {
		return d.cfg.ExistingInstance.SSHKey
	}
	return d.sshKey.path
}

func (d *driver) prepareInstance(ctx context.Context) error {
	log := clog.FromContext(ctx)

	// If no startup_script, nothing to wait for
	if d.cfg.StartupScript == "" {
		log.Info("no startup_script provided, skipping startup script wait")
		return nil
	}

	signer, err := d.sshSigner()
	if err != nil {
		return fmt.Errorf("getting SSH signer: %w", err)
	}

	// GCE startup scripts are executed by the google-startup-scripts systemd
	// service, which runs independently of cloud-init. We wait for that service
	// to complete, with retry logic to handle SSH not being ready yet or reboots.
	log.Info("waiting for GCE startup script to complete")

	backoff := wait.Backoff{
		Duration: 10 * time.Second,
		Factor:   1.0, // constant delay
		Steps:    90,  // 15 min max
	}

	var attempt int
	return wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		attempt++

		conn, err := issh.Connect(d.instanceIP(), uint16(d.cfg.SSHPort), d.cfg.SSHUser, signer)
		if err != nil {
			log.Info("SSH not ready, retrying", "attempt", attempt, "error", err)
			return false, nil // retry
		}
		defer conn.Close()

		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)

		// On GCE, startup-script metadata is executed by the
		// google-startup-scripts.service systemd unit. This is a oneshot
		// service that may not have started yet when we first SSH in.
		//
		// Strategy: use `systemctl start --wait` which will:
		// - Start the service if it hasn't started yet
		// - Wait for it to complete if it's currently running
		// - Return immediately if it already ran (oneshot + RemainAfterExit=no)
		//
		// We check the exit status via the journal afterwards.
		err = issh.ExecIn(conn, issh.ShellBash, stdout, stderr,
			"sudo systemctl start google-startup-scripts.service 2>/dev/null; "+
				"while [ \"$(sudo systemctl show google-startup-scripts.service --property=ActiveState --value)\" = \"activating\" ]; do sleep 5; done; "+
				"result=$(sudo systemctl show google-startup-scripts.service --property=Result --value 2>/dev/null); "+
				"echo \"startup script result: $result\"; "+
				"if [ \"$result\" != \"success\" ]; then "+
				"sudo journalctl -u google-startup-scripts.service --no-pager -n 20; exit 1; fi")
		if err != nil {
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				if exitErr.ExitStatus() == 1 {
					log.Error("startup script failed", "stdout", stdout.String(), "stderr", stderr.String())
					return false, fmt.Errorf("startup script failed (check instance serial console): %s", stdout.String())
				}
				log.Info("startup script check returned non-zero, retrying", "attempt", attempt, "exit_code", exitErr.ExitStatus())
				return false, nil
			}
			log.Info("startup script check interrupted, retrying", "attempt", attempt, "error", err)
			return false, nil
		}

		log.Info("startup script complete", "output", stdout.String())
		return true, nil
	})
}

func (d *driver) runSetupCommands(ctx context.Context) error {
	if len(d.cfg.SetupCommands) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)

	signer, err := d.sshSigner()
	if err != nil {
		return fmt.Errorf("getting SSH signer: %w", err)
	}

	conn, err := issh.Connect(d.instanceIP(), uint16(d.cfg.SSHPort), d.cfg.SSHUser, signer)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer conn.Close()

	cmds := []string{cmdStdOpts}
	for k, v := range d.cfg.Env {
		cmds = append(cmds, fmt.Sprintf("export %s=%s", k, shellquote.Join(v)))
	}
	cmds = append(cmds, d.cfg.SetupCommands...)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	log.Info("running setup commands", "count", len(d.cfg.SetupCommands))
	if err := issh.ExecIn(conn, d.cfg.Shell, stdout, stderr, cmds...); err != nil {
		log.Error("setup commands failed", "stdout", stdout.String(), "stderr", stderr.String())
		return fmt.Errorf("setup commands failed: %w", err)
	}

	log.Info("setup commands complete")
	return nil
}

// sanitizeGCELabelValue sanitizes a string to be a valid GCE label value.
// GCE labels allow: lowercase letters, digits, hyphens, underscores. Max 63 chars.
// Values can be empty, but if non-empty they must start and end with alphanumeric.
// https://cloud.google.com/compute/docs/labeling-resources#requirements
func sanitizeGCELabelValue(s string, maxLen int) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= maxLen {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// sanitizeGCEName sanitizes a string to be a valid GCE resource name.
// GCE names: [a-z]([-a-z0-9]*[a-z0-9])?, max 63 chars.
// https://cloud.google.com/compute/docs/naming-resources#resource-name-format
// All characters are ASCII so byte-level operations are safe.
var reInvalidGCEName = regexp.MustCompile(`[^a-z0-9-]`)

func sanitizeGCEName(s string) string {
	s = strings.ToLower(s)
	s = reInvalidGCEName.ReplaceAllString(s, "-")
	// Must start with a letter
	if len(s) > 0 && (s[0] < 'a' || s[0] > 'z') {
		s = "i" + s
	}
	if len(s) > 63 {
		s = s[:63]
	}
	// Must end with alphanumeric
	s = strings.TrimRight(s, "-")
	return s
}

func buildLabels(testName string) map[string]string {
	labels := map[string]string{
		"imagetest":           "true",
		"imagetest-driver":    "gce",
		"imagetest-test-name": sanitizeGCELabelValue(testName, 63),
		"imagetest-expires":   sanitizeGCELabelValue(time.Now().Add(2*time.Hour).Format(time.RFC3339), 63),
		"team":                "containers",
		"project":             "terraform-provider-imagetest",
	}

	// Add attribution labels based on environment
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		if v := os.Getenv("GITHUB_ACTOR"); v != "" {
			labels["imagetest-triggered-by"] = sanitizeGCELabelValue(v, 63)
		}
		if v := os.Getenv("GITHUB_REPOSITORY"); v != "" {
			labels["imagetest-repository"] = sanitizeGCELabelValue(v, 63)
		}
		if v := os.Getenv("GITHUB_RUN_ID"); v != "" {
			labels["imagetest-run-id"] = sanitizeGCELabelValue(v, 63)
		}
		if v := os.Getenv("GITHUB_SHA"); v != "" {
			if len(v) > 8 {
				v = v[:8]
			}
			labels["imagetest-commit"] = sanitizeGCELabelValue(v, 63)
		}
	} else {
		if v := os.Getenv("USER"); v != "" {
			labels["imagetest-triggered-by"] = sanitizeGCELabelValue(v, 63)
		}
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			labels["imagetest-hostname"] = sanitizeGCELabelValue(hostname, 63)
		}
	}

	return labels
}
