package ec2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
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

// resource represents an AWS resource that can be created and torn down.
type resource interface {
	create(ctx context.Context) (Teardown, error)
}

type driver struct {
	name  string
	cfg   Config
	stack *harness.Stack

	ec2 *ec2.Client
	iam *iam.Client

	// Runtime state for normal mode
	subnet   *subnet
	sg       *securityGroup
	key      *keyPair
	profile  *instanceProfile
	instance *instance

	// Runtime state for existing instance mode
	existingSigner ssh.Signer
	existingIP     string
}

func NewDriver(cfg Config, ec2Client *ec2.Client, iamClient *iam.Client) (*driver, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &driver{
		cfg:   cfg,
		stack: harness.NewStack(),
		ec2:   ec2Client,
		iam:   iamClient,
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

	d.name = "imagetest-ec2-" + uuid.New().String()[:8]
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

func (d *driver) setupNewInstance(ctx context.Context) error {
	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	d.name = "imagetest-ec2-" + uuid.New().String()[:8]
	log.Info("setting up EC2 driver", "name", d.name, "vpc_id", d.cfg.VPCID)

	d.subnet = &subnet{
		client: d.ec2,
		vpcID:  d.cfg.VPCID,
		region: d.cfg.Region,
		cidr:   d.cfg.SubnetCIDR,
		tags:   d.buildTags(d.name + "-subnet"),
	}
	if err := d.create(ctx, d.subnet); err != nil {
		return fmt.Errorf("creating subnet: %w", err)
	}

	d.sg = &securityGroup{
		client:  d.ec2,
		vpcID:   d.cfg.VPCID,
		name:    d.name + "-sg",
		sshPort: d.cfg.SSHPort,
		tags:    d.buildTags(d.name + "-sg"),
	}
	if err := d.create(ctx, d.sg); err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}

	d.key = &keyPair{
		client: d.ec2,
		name:   d.name + "-key",
		tags:   d.buildTags(d.name + "-key"),
	}
	if err := d.create(ctx, d.key); err != nil {
		return fmt.Errorf("creating key pair: %w", err)
	}

	profileName := d.cfg.InstanceProfileName
	if profileName == "" {
		d.profile = &instanceProfile{
			client:     d.iam,
			namePrefix: d.name,
			tags:       d.buildIAMTags(d.name + "-profile"),
		}
		if err := d.create(ctx, d.profile); err != nil {
			return fmt.Errorf("creating instance profile: %w", err)
		}
		profileName = d.profile.profileName
	}

	d.instance = &instance{
		client:          d.ec2,
		ami:             d.cfg.AMI,
		instanceType:    d.cfg.instanceType(),
		rootVolumeSize:  d.cfg.RootVolumeSize,
		subnetID:        d.subnet.id,
		securityGroupID: d.sg.id,
		keyName:         d.key.name,
		profileName:     profileName,
		userData:        d.cfg.UserData,
		sshPort:         d.cfg.SSHPort,
		tags:            d.buildTags(d.name + "-instance"),
	}
	if err := d.create(ctx, d.instance); err != nil {
		return fmt.Errorf("creating instance: %w", err)
	}
	span.AddEvent("ec2.instance.created")

	if err := d.instance.wait(ctx); err != nil {
		return fmt.Errorf("waiting for instance: %w", err)
	}
	span.AddEvent("ec2.instance.running")

	if err := d.prepareInstance(ctx); err != nil {
		return fmt.Errorf("preparing instance: %w", err)
	}
	span.AddEvent("ec2.cloudinit.complete")

	if err := d.runSetupCommands(ctx); err != nil {
		return fmt.Errorf("running setup commands: %w", err)
	}
	span.AddEvent("ec2.setup.complete")

	log.Info("EC2 driver setup complete", "instance_id", d.instance.id, "public_ip", d.instance.publicIP)

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set - resources will not be cleaned up",
			"instance_id", d.instance.id,
			"instance_ip", d.instance.publicIP,
			"ssh_key", d.sshKeyPath(),
			"ssh_user", d.cfg.SSHUser,
		)
		log.Infof("to connect: ssh -i %s %s@%s", d.sshKeyPath(), d.cfg.SSHUser, d.instance.publicIP)
	}

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

func (d *driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)

	if d.cfg.ExistingInstance != nil {
		log.Info("existing instance mode - nothing to teardown")
		return nil
	}

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set, skipping teardown",
			"instance_id", d.instance.id,
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
		return nil, fmt.Errorf("docker is not accessible on the instance (ensure Docker is installed and running via user_data or setup_commands): %w", err)
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
	return d.key.private.ToSSH()
}

// sshKeyPath returns the path to the SSH key file.
func (d *driver) sshKeyPath() string {
	if d.cfg.ExistingInstance != nil {
		return d.cfg.ExistingInstance.SSHKey
	}
	return d.key.path
}

func (d *driver) prepareInstance(ctx context.Context) error {
	log := clog.FromContext(ctx)

	// If no user_data, nothing to wait for
	if d.cfg.UserData == "" {
		log.Info("no user_data provided, skipping cloud-init wait")
		return nil
	}

	signer, err := d.sshSigner()
	if err != nil {
		return fmt.Errorf("getting SSH signer: %w", err)
	}

	// Wait for cloud-init with retry logic to handle reboots
	// Cloud-init may reboot the instance (e.g., after driver install), which drops SSH
	log.Info("waiting for cloud-init to complete (may involve reboot)")

	backoff := wait.Backoff{
		Duration: 10 * time.Second,
		Factor:   1.0, // constant delay - we're just waiting for potentnial reboots, not backing off
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

		err = issh.ExecIn(conn, issh.ShellBash, stdout, stderr, "sudo cloud-init status --wait")
		if err != nil {
			// Check if this is an exit error (command ran but failed) vs connection error
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				// cloud-init exit codes: 0=success, 1=critical failure, 2=recoverable failure
				// Either way, cloud-init is done - don't retry
				log.Error("cloud-init failed", "exit_code", exitErr.ExitStatus(), "stdout", stdout.String(), "stderr", stderr.String())
				return false, fmt.Errorf("cloud-init failed with exit code %d (check /var/log/cloud-init-output.log)", exitErr.ExitStatus())
			}
			// Connection error - likely rebooting, retry
			log.Info("cloud-init check interrupted, retrying", "attempt", attempt, "error", err)
			return false, nil
		}

		log.Info("cloud-init complete")
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

// sanitizeAWSTagValue sanitizes a string to be a valid AWS tag value.
// AWS tag values allow: Unicode letters, digits, whitespace, and _ . : / = + - @
// Max length is 256 characters for values, 128 for keys.
func sanitizeAWSTagValue(s string, maxLen int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return r
		}
		switch r {
		case '_', '.', ':', '/', '=', '+', '-', '@':
			return r
		}
		return -1
	}, s)
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

func (d *driver) baseTags(name string) map[string]string {
	tags := map[string]string{
		"Name":                name,
		"imagetest":           "true",
		"imagetest:driver":    "ec2",
		"imagetest:test-name": d.name,
		"imagetest:expires":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		"Team":                "Containers",
		"Project":             "terraform-provider-imagetest",
	}

	// Add attribution tags based on environment
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		if v := os.Getenv("GITHUB_ACTOR"); v != "" {
			tags["imagetest:triggered-by"] = v
		}
		if v := os.Getenv("GITHUB_REPOSITORY"); v != "" {
			tags["imagetest:repository"] = v
		}
		if v := os.Getenv("GITHUB_RUN_ID"); v != "" {
			tags["imagetest:run-id"] = v
		}
		if v := os.Getenv("GITHUB_SHA"); v != "" {
			if len(v) > 8 {
				v = v[:8]
			}
			tags["imagetest:commit"] = v
		}
	} else {
		if v := os.Getenv("USER"); v != "" {
			tags["imagetest:triggered-by"] = v
		}
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			tags["imagetest:hostname"] = hostname
		}
	}

	return tags
}

func (d *driver) buildTags(name string) []ec2types.Tag {
	base := d.baseTags(name)
	tags := make([]ec2types.Tag, 0, len(base))
	for k, v := range base {
		k = sanitizeAWSTagValue(k, 128)
		if k == "" {
			continue
		}
		tags = append(tags, ec2types.Tag{
			Key:   aws.String(k),
			Value: aws.String(sanitizeAWSTagValue(v, 256)),
		})
	}
	return tags
}

func (d *driver) buildIAMTags(name string) []iamtypes.Tag {
	base := d.baseTags(name)
	tags := make([]iamtypes.Tag, 0, len(base))
	for k, v := range base {
		k = sanitizeAWSTagValue(k, 128)
		if k == "" {
			continue
		}
		tags = append(tags, iamtypes.Tag{
			Key:   aws.String(k),
			Value: aws.String(sanitizeAWSTagValue(v, 256)),
		})
	}
	return tags
}
