package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sshhost"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

type driver struct {
	name  string
	cfg   Config
	stack *harness.Stack

	// host is set by Setup once the key and the pinned host key are
	// loaded, and stays nil when Setup fails before that.
	host *sshhost.Host
}

func NewDriver(cfg Config) (*driver, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &driver{
		cfg:   cfg,
		stack: harness.NewStack(),
	}, nil
}

func (d *driver) Setup(ctx context.Context) error {
	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	d.name = "imagetest-ssh-" + uuid.New().String()[:8]
	log.Info("setting up ssh driver", "name", d.name, "host", d.cfg.Host)

	signer, err := sshhost.ReadKey(d.cfg.SSHKey)
	if err != nil {
		return err
	}

	hostKeys, err := sshhost.LoadKnownHosts(d.cfg.KnownHosts, d.cfg.Host, d.cfg.Port)
	if err != nil {
		return fmt.Errorf("loading known_hosts: %w", err)
	}
	log.Info("host key pinned", "known_hosts", d.cfg.KnownHosts, "keys", len(hostKeys))

	d.host = &sshhost.Host{
		Name:          d.name,
		Driver:        "ssh",
		Addr:          d.cfg.Host,
		Port:          d.cfg.Port,
		User:          d.cfg.User,
		KeyPath:       d.cfg.SSHKey,
		Signer:        signer,
		KnownHosts:    d.cfg.KnownHosts,
		HostKeys:      hostKeys,
		Shell:         d.cfg.Shell,
		Env:           d.cfg.Env,
		SetupCommands: d.cfg.SetupCommands,
		OnFailure:     d.cfg.OnFailure,
		GPUs:          d.cfg.GPUs,
		VolumeMounts:  d.cfg.VolumeMounts,
		DeviceMounts:  d.cfg.DeviceMounts,
		Stack:         d.stack,
	}

	// One connection up front, so a wrong key or a wrong pin fails here
	// and not inside the Docker tunnel's OpenSSH process.
	conn, err := d.host.Connect()
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", d.cfg.Host, err)
	}
	_ = conn.Close()
	span.AddEvent("ssh.connected")

	if err := d.host.RunSetupCommands(ctx); err != nil {
		return fmt.Errorf("running setup commands: %w", err)
	}

	d.host.CollectGPUInfo(ctx)

	// Pull the images under test into the host's daemon so the tests find
	// them locally. The host holds no registry credentials.
	if err := d.host.PrePullImages(ctx, d.cfg.PrePullImages); err != nil {
		return fmt.Errorf("pre-pulling images: %w", err)
	}
	span.AddEvent("ssh.setup.complete")

	log.Info("ssh driver setup complete", "host", d.cfg.Host)
	return nil
}

func (d *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	if d.host == nil {
		return nil, errors.New("host is not set up")
	}
	return d.host.Run(ctx, ref)
}

// Teardown removes the test containers. The host is shared and persistent,
// so nothing else is touched.
func (d *driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set, leaving test containers on the host",
			"host", d.cfg.Host,
			"ssh_key", d.cfg.SSHKey,
			"ssh_user", d.cfg.User,
		)
		log.Infof("to connect: ssh -i %s %s@%s", d.cfg.SSHKey, d.cfg.User, d.cfg.Host)
		return nil
	}

	log.Info("starting teardown")
	ctx, cancel := d.cfg.Timeouts.TeardownContext(ctx)
	defer cancel()
	return d.stack.Teardown(ctx)
}

// Diagnose implements drivers.Diagnoser by running the configured on_failure
// commands on the host.
func (d *driver) Diagnose(ctx context.Context, w io.Writer) error {
	if len(d.cfg.OnFailure) == 0 {
		return nil
	}
	// Setup may have failed before the key or the pinned host key loaded.
	if d.host == nil {
		return errors.New("host was never reached")
	}
	return d.host.Diagnose(ctx, w)
}
