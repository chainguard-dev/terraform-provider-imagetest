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

	// releaseLock releases the host lock Setup took, and is nil without
	// lock_path or when Setup failed before the lock. It is kept off the
	// stack so Teardown can release the lock after the stack's deadline.
	releaseLock func(context.Context) error
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
	ctx, cancel := d.cfg.Timeouts.SetupContext(ctx)
	defer cancel()

	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	d.name = "imagetest-ssh-" + uuid.New().String()[:8]
	log.Info("setting up ssh driver", "name", d.name, "host", d.cfg.Host)

	host := &sshhost.Host{
		Name:          d.name,
		Driver:        "ssh",
		Addr:          d.cfg.Host,
		Port:          d.cfg.SSHPort,
		User:          d.cfg.SSHUser,
		KeyPath:       d.cfg.SSHKey,
		KnownHosts:    d.cfg.KnownHosts,
		Shell:         d.cfg.Shell,
		Env:           d.cfg.Env,
		SetupCommands: d.cfg.SetupCommands,
		OnFailure:     d.cfg.OnFailure,
		GPUs:          d.cfg.GPUs,
		VolumeMounts:  d.cfg.VolumeMounts,
		DeviceMounts:  d.cfg.DeviceMounts,
		LockPath:      d.cfg.LockPath,
		Stack:         d.stack,

		PullECRWithHostCredentials: d.cfg.PullECRWithHostCredentials,
	}
	if err := host.Load(); err != nil {
		return err
	}
	log.Info("host key pinned", "known_hosts", d.cfg.KnownHosts, "keys", len(host.HostKeys))
	d.host = host

	// One connection up front, so a wrong key or a wrong pin fails here
	// and not inside the Docker tunnel's OpenSSH process. The typed error
	// tells callers nothing ran on the host.
	conn, err := d.host.Connect(ctx)
	if err != nil {
		return &SSHConnectionError{Host: d.cfg.Host, Port: d.cfg.SSHPort, Err: err}
	}
	_ = conn.Close()
	span.AddEvent("ssh.connected")

	// The lock is released by Teardown after the stack, so after the test
	// containers and the run sweep. While it is held no other run on the
	// same lock can have live containers on the host, so what such a run
	// left behind is stale and is removed now. A container that will not
	// go is logged, not fatal: failing here would fail every later run on
	// the host until an operator steps in, and the test itself shows
	// whether the host is usable.
	if d.cfg.LockPath != "" {
		lock, err := d.host.AcquireLock(ctx)
		if err != nil {
			return fmt.Errorf("acquiring lock: %w", err)
		}
		d.releaseLock = lock.Release
		if err := d.host.RemoveStaleContainers(ctx); err != nil {
			log.Warn("stale containers were not all removed, continuing", "error", err)
		}
	}

	// Setup commands run before the GPU probe, unlike the ec2 launch path:
	// on a held host they are where a missing driver or toolkit gets
	// installed, so the probe reports the state the tests will see.
	if err := d.host.RunSetupCommands(ctx); err != nil {
		return fmt.Errorf("running setup commands: %w", err)
	}

	d.host.CollectGPUInfo(ctx)

	// Pull the images under test into the host's daemon so the tests find
	// them locally, with the caller's registry credentials or, for ECR
	// images when configured, with the host's own.
	if err := d.host.PrePullImages(ctx, d.cfg.PrePullImages); err != nil {
		return fmt.Errorf("pre-pulling images: %w", err)
	}

	// Pushed before any test container, so it runs after their removal:
	// it takes out what the tests started with the run's label. Pushed
	// last in Setup, so a Setup that fails before this point does not
	// spend its teardown waiting for a Docker daemon it never reached.
	if err := d.stack.Add(d.host.RemoveRunContainers); err != nil {
		return fmt.Errorf("adding run sweep to stack: %w", err)
	}
	span.AddEvent("ssh.setup.complete")

	log.Info("ssh driver setup complete", "host", d.cfg.Host)

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set - test containers will be left on the host",
			"host", d.cfg.Host,
			"ssh_key", d.cfg.SSHKey,
			"ssh_user", d.cfg.SSHUser,
		)
		log.Infof("to connect: ssh -i %s -p %d %s@%s", d.cfg.SSHKey, d.cfg.SSHPort, d.cfg.SSHUser, d.cfg.Host)
	}
	return nil
}

func (d *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	if d.host == nil {
		return nil, errors.New("host is not set up")
	}
	return d.host.Run(ctx, ref)
}

// Teardown removes the test containers, then every container labelled with
// the run, then releases the host lock. The host is shared and persistent,
// so teardown touches only containers.
//
// The lock is released whatever came before it. The stack stops at its
// context's deadline, and a lock kept past that holds up every other run on
// the host until this process exits, so the release runs outside that
// context and is bounded by the lock's own release timeout. With
// IMAGETEST_SKIP_TEARDOWN the containers stay and only the lock is
// released.
func (d *driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)
	defer func() {
		if d.releaseLock == nil {
			return
		}
		if err := d.releaseLock(context.WithoutCancel(ctx)); err != nil {
			log.Warn("failed to release the host lock", "error", err)
		}
	}()

	if d.cfg.SkipTeardown {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set, skipping teardown", "host", d.cfg.Host)
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
		return errors.New("SSH key or known_hosts could not be loaded, nothing to connect with")
	}
	return d.host.Diagnose(ctx, w)
}
