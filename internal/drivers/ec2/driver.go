package ec2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
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
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sshhost"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
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

	// host is the instance as a Docker host over SSH. It is set once the
	// instance is reachable: after launch, or at Setup in existing instance
	// mode, and stays nil when Setup fails before that.
	host *sshhost.Host
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
	ctx, cancel := d.cfg.Timeouts.SetupContext(ctx)
	defer cancel()

	if d.cfg.ExistingInstance != nil {
		return d.setupExistingInstance(ctx)
	}
	return d.setupNewInstance(ctx)
}

func (d *driver) setupExistingInstance(ctx context.Context) error {
	log := clog.FromContext(ctx)

	d.name = "imagetest-ec2-" + uuid.New().String()[:8]
	log.Info("using existing instance", "name", d.name, "ip", d.cfg.ExistingInstance.IP)

	host := d.newHost(d.cfg.ExistingInstance.IP, d.cfg.ExistingInstance.SSHKey)
	host.KnownHosts = d.cfg.ExistingInstance.KnownHosts
	if err := host.Load(); err != nil {
		return err
	}
	if host.KnownHosts != "" {
		log.Info("host key pinned", "known_hosts", host.KnownHosts, "keys", len(host.HostKeys))
	}
	d.host = host

	// Run setup commands if any
	if err := d.host.RunSetupCommands(ctx); err != nil {
		return fmt.Errorf("running setup commands: %w", err)
	}

	// Pull the images under test into the instance's daemon so the tests
	// find them locally. The instance holds no registry credentials.
	if err := d.host.PrePullImages(ctx, d.cfg.PrePullImages); err != nil {
		return fmt.Errorf("pre-pulling images: %w", err)
	}

	log.Info("existing instance ready", "ip", d.host.Addr)
	return nil
}

// newHost returns the instance at addr as a Docker host over SSH, with the
// driver's shell, env, setup and container settings. The caller loads the
// key, or sets Signer for a key it already holds.
func (d *driver) newHost(addr, keyPath string) *sshhost.Host {
	return &sshhost.Host{
		Name:          d.name,
		Driver:        "ec2",
		Addr:          addr,
		Port:          d.cfg.SSHPort,
		User:          d.cfg.SSHUser,
		KeyPath:       keyPath,
		Shell:         d.cfg.Shell,
		Env:           d.cfg.Env,
		SetupCommands: d.cfg.SetupCommands,
		OnFailure:     d.cfg.OnFailure,
		GPUs:          d.cfg.GPUs,
		VolumeMounts:  d.cfg.VolumeMounts,
		DeviceMounts:  d.cfg.DeviceMounts,
		Stack:         d.stack,
	}
}

func (d *driver) setupNewInstance(ctx context.Context) error {
	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	d.name = "imagetest-ec2-" + uuid.New().String()[:8]
	log.Info("setting up EC2 driver", "name", d.name, "vpc_id", d.cfg.VPCID)

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

	if err := d.createPlacement(ctx, profileName); err != nil {
		return err
	}
	span.AddEvent("ec2.instance.created")

	if err := d.instance.wait(ctx); err != nil {
		return fmt.Errorf("waiting for instance: %w", err)
	}
	span.AddEvent("ec2.instance.running")

	d.host = d.newHost(d.instance.addr(), d.key.path)
	signer, err := d.key.private.ToSSH()
	if err != nil {
		return fmt.Errorf("getting SSH signer: %w", err)
	}
	d.host.Signer = signer
	// The instance profile the driver creates can read ECR, and a custom
	// instance_profile_name has to as well. ECR images are pulled with it
	// rather than the caller's registry credentials, which may not be usable
	// from the instance.
	d.host.PullECRWithHostCredentials = true

	if err := d.prepareInstance(ctx); err != nil {
		return fmt.Errorf("preparing instance: %w", err)
	}
	span.AddEvent("ec2.cloudinit.complete")

	d.host.CollectGPUInfo(ctx)

	if err := d.host.RunSetupCommands(ctx); err != nil {
		return fmt.Errorf("running setup commands: %w", err)
	}
	span.AddEvent("ec2.setup.complete")

	log.Info("EC2 driver setup complete", "instance_id", d.instance.id, "public_ip", d.instance.publicIP)

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set - resources will not be cleaned up",
			"instance_id", d.instance.id,
			"instance_addr", d.instance.addr(),
			"ssh_key", d.sshKeyPath(),
			"ssh_user", d.cfg.SSHUser,
		)
		log.Infof("to connect: ssh -i %s %s@%s", d.sshKeyPath(), d.cfg.SSHUser, d.instance.addr())
	}

	return nil
}

// azSuffixes are the availability zone suffixes to try. Not every region has
// every zone (or offers the instance type in every zone) - attempts against
// an unusable zone fail fast and the next one is tried.
var azSuffixes = []string{"a", "b", "c", "d"}

// createPlacement launches the instance, trying availability zones in random
// order until one works. Subnets are AZ-bound, so each attempt gets its own
// subnet; a failed attempt's subnet is deleted immediately and never
// registered on the teardown stack - only the successful subnet+instance pair
// lands there, as a single teardown that terminates the instance before
// deleting its subnet.
func (d *driver) createPlacement(ctx context.Context, profileName string) error {
	log := clog.FromContext(ctx)

	candidates := make([]string, len(azSuffixes))
	for i, suffix := range azSuffixes {
		candidates[i] = d.cfg.Region + suffix
	}
	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	for _, az := range candidates {
		sn := &subnet{
			client: d.ec2,
			vpcID:  d.cfg.VPCID,
			az:     az,
			cidr:   d.cfg.SubnetCIDR,
			tags:   d.buildTags(d.name + "-subnet"),
		}
		snTeardown, err := sn.create(ctx)
		if err != nil {
			if isAZRetryable(err) {
				log.Warn("availability zone unusable, trying next", "az", az, "error", err)
				continue
			}
			return fmt.Errorf("creating subnet in %s: %w", az, err)
		}

		inst := &instance{
			client:          d.ec2,
			ami:             d.cfg.AMI,
			instanceType:    d.cfg.instanceType(),
			rootVolumeSize:  d.cfg.RootVolumeSize,
			subnetID:        sn.id,
			securityGroupID: d.sg.id,
			keyName:         d.key.name,
			profileName:     profileName,
			userData:        d.cfg.UserData,
			sshPort:         d.cfg.SSHPort,
			tags:            d.buildTags(d.name + "-instance"),
		}
		instTeardown, err := inst.create(ctx)
		if err == nil {
			d.subnet = sn
			d.instance = inst
			if err := d.stack.Add(func(ctx context.Context) error {
				// Instance first: its teardown waits for termination so the
				// ENI is released before the subnet can be deleted.
				ierr := instTeardown(ctx)
				serr := snTeardown(ctx)
				return errors.Join(ierr, serr)
			}); err != nil {
				return fmt.Errorf("adding teardown to stack: %w", err)
			}
			return nil
		}

		// This attempt's subnet is not on the stack - clean it up now.
		if terr := snTeardown(ctx); terr != nil {
			log.Warn("cleaning up subnet after failed launch", "az", az, "error", terr)
		}

		if !isAZRetryable(err) {
			return fmt.Errorf("creating instance: %w", err)
		}
		log.Warn("availability zone unusable, trying next",
			"instance_type", d.cfg.instanceType(), "az", az, "error", err)
	}

	return fmt.Errorf("no availability zone in %s could run instance type %s (tried %v)",
		d.cfg.Region, d.cfg.instanceType(), candidates)
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

var _ drivers.LocalFiler = &driver{}

// LocalFiles implements drivers.LocalFiler. It reports only the private key
// this driver generated for its key pair; in existing-instance mode there is
// no key pair and the user's own SSH key is never reported.
func (d *driver) LocalFiles() []drivers.LocalFile {
	if d.key == nil || d.key.path == "" {
		return nil
	}
	return []drivers.LocalFile{{
		Path:        d.key.path,
		Description: "SSH private key for EC2 key pair " + d.key.name,
	}}
}

func (d *driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set, skipping teardown",
			"instance_addr", d.instanceAddr(),
			"ssh_key", d.sshKeyPath(),
			"ssh_user", d.cfg.SSHUser,
		)
		log.Infof("to connect: ssh -i %s %s@%s", d.sshKeyPath(), d.cfg.SSHUser, d.instanceAddr())
		return nil
	}

	// In existing instance mode the stack holds only the test containers'
	// removal. The instance is shared and persistent, so they must not be
	// left behind.
	log.Info("starting teardown")
	ctx, cancel := d.cfg.Timeouts.TeardownContext(ctx)
	defer cancel()
	return d.stack.Teardown(ctx)
}

func (d *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	if d.host == nil {
		return nil, errors.New("instance is not set up")
	}
	return d.host.Run(ctx, ref)
}

// Diagnose implements drivers.Diagnoser by running the configured on_failure
// commands on the instance.
func (d *driver) Diagnose(ctx context.Context, w io.Writer) error {
	if len(d.cfg.OnFailure) == 0 {
		return nil
	}
	// Setup may have failed before the instance or the SSH key existed.
	if d.host == nil {
		return errors.New("instance was not created or never became reachable")
	}
	return d.host.Diagnose(ctx, w)
}

// instanceAddr returns the address the driver connects to, a DNS name or an
// IP, or "" before the instance is reachable.
func (d *driver) instanceAddr() string {
	if d.host == nil {
		return ""
	}
	return d.host.Addr
}

// sshKeyPath returns the path to the SSH key file, or "" before it exists.
func (d *driver) sshKeyPath() string {
	if d.cfg.ExistingInstance != nil {
		return d.cfg.ExistingInstance.SSHKey
	}
	if d.key == nil {
		return ""
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

		conn, err := d.host.Connect(ctx)
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
			if exitErr, ok := errors.AsType[*ssh.ExitError](err); ok {
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
