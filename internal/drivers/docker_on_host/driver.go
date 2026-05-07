package dockeronhost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/bundler"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/uuid"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/trace"
)

// LabelKey is the label key applied to every sandbox container and expected
// to be applied by tests to any sibling container they spawn, so the driver
// can clean them all up on teardown.
const LabelKey = "imagetest.test"

// EnvLabelVar is the env var the driver sets in the sandbox so test scripts
// can pass `--label "$IMAGETEST_TEST_LABEL"` without hardcoding the value.
const EnvLabelVar = "IMAGETEST_TEST_LABEL"

// ExtraMount is an additional host bind mount beyond the defaults
// (/var/run/docker.sock, /sys/fs/cgroup, /tmp).
type ExtraMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

type driver struct {
	ImageRef    name.Reference
	Envs        map[string]string
	ExtraHosts  []string
	ExtraMounts []ExtraMount

	name     string
	stack    *harness.Stack
	cli      *docker.Client
	cliCfg   *docker.DockerConfig
	ropts    []remote.Option
	timeouts drivers.Timeouts
}

func NewDriver(n string, opts ...DriverOpts) (drivers.Tester, error) {
	d := &driver{
		ImageRef: name.MustParseReference("cgr.dev/chainguard/docker-dind:latest"),
		name:     n,
		stack:    harness.NewStack(),
		ropts: []remote.Option{
			remote.WithPlatform(ggcrv1.Platform{
				OS:           "linux",
				Architecture: runtime.GOARCH,
			}),
		},
		cliCfg: &docker.DockerConfig{},
	}

	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}

	return d, nil
}

// Setup implements drivers.Tester.
func (d *driver) Setup(ctx context.Context) error {
	cli, err := docker.New()
	if err != nil {
		return err
	}
	d.cli = cli
	return nil
}

// Teardown implements drivers.Tester.
func (d *driver) Teardown(ctx context.Context) error {
	ctx, cancel := d.timeouts.TeardownContext(ctx)
	defer cancel()
	return d.stack.Teardown(ctx)
}

// Run implements drivers.Tester.
func (d *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	span := trace.SpanFromContext(ctx)

	tref, err := bundler.Mutate(ctx, d.ImageRef, ref.Context(), bundler.MutateOpts{
		RemoteOptions: d.ropts,
		ImageMutators: []func(ggcrv1.Image) (ggcrv1.Image, error){
			func(base ggcrv1.Image) (ggcrv1.Image, error) {
				timg, err := remote.Image(ref, d.ropts...)
				if err != nil {
					return nil, fmt.Errorf("failed to load test image: %w", err)
				}

				layers, err := timg.Layers()
				if err != nil {
					return nil, fmt.Errorf("failed to get layers: %w", err)
				}

				mutated, err := mutate.AppendLayers(base, layers...)
				if err != nil {
					return nil, fmt.Errorf("failed to append layers: %w", err)
				}

				mcfgf, err := mutated.ConfigFile()
				if err != nil {
					return nil, fmt.Errorf("failed to get config file: %w", err)
				}

				tcfgf, err := timg.ConfigFile()
				if err != nil {
					return nil, fmt.Errorf("failed to get config file: %w", err)
				}

				mcfgf.Config.Entrypoint = tcfgf.Config.Entrypoint
				mcfgf.Config.Cmd = tcfgf.Config.Cmd
				mcfgf.Config.WorkingDir = tcfgf.Config.WorkingDir
				mcfgf.Config.Env = append(mcfgf.Config.Env, tcfgf.Config.Env...)

				return mutate.ConfigFile(mutated, mcfgf)
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build driver image: %w", err)
	}
	span.AddEvent("doh.image.built")

	cliCfg, err := d.cliCfg.Content()
	if err != nil {
		return nil, err
	}

	contents := []*docker.Content{
		cliCfg,
		docker.NewExecutableContentFromString(dockerShim, shimPath),
	}

	r, w := io.Pipe()
	defer w.Close()

	stw := bytes.NewBuffer(nil)
	mw := io.MultiWriter(w, stw)

	go func() {
		defer r.Close()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			clog.InfoContext(ctx, scanner.Text())
		}
	}()

	// Use the sandbox container name (which already includes a uuid suffix)
	// as the run-unique label value, so parallel runs of the same test id
	// don't tear each other's siblings down.
	cname := fmt.Sprintf("%s-%s", d.name, uuid.New().String()[:8])
	runLabel := cname

	extraHosts := []string{"host.docker.internal:host-gateway"}
	extraHosts = append(extraHosts, d.ExtraHosts...)

	envs := []string{
		fmt.Sprintf("%s=%s=%s", EnvLabelVar, LabelKey, runLabel),
	}
	for k, v := range d.Envs {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}

	mounts := []mount.Mount{
		// Sibling containers: route to the host daemon.
		{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
	}
	for _, m := range d.ExtraMounts {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	// Register sibling cleanup before the sandbox is started, so siblings
	// created during a partially-failed Run() are also reaped on Teardown.
	if err := d.stack.Add(func(ctx context.Context) error {
		return d.cli.RemoveByLabel(ctx, LabelKey, runLabel)
	}); err != nil {
		return nil, err
	}

	clog.InfoContext(ctx, "running docker-on-host test", "image_ref", tref.String(), "container_name", cname, "label", runLabel)
	span.AddEvent("doh.container.started")
	cid, err := d.cli.Run(ctx, &docker.Request{
		Name:       cname,
		Ref:        tref,
		User:       "0:0",
		Mounts:     mounts,
		AutoRemove: false,
		HealthCheck: &v1.HealthcheckConfig{
			Test:        append([]string{"CMD"}, entrypoint.DefaultHealthCheckCommand...),
			Interval:    1 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     1,
			StartPeriod: 1 * time.Second,
		},
		Env:        envs,
		ExtraHosts: extraHosts,
		Contents:   contents,
		Logger:     mw,
		Labels: map[string]string{
			LabelKey: runLabel,
		},
	})

	result := &drivers.RunResult{}
	span.AddEvent("doh.container.completed")

	arc, aerr := docker.GetFile(ctx, d.cli, cid, entrypoint.ArtifactsPath)
	if aerr != nil {
		clog.WarnContextf(ctx, "failed to retrieve artifact: %v", aerr)
	} else {
		a, aerr := drivers.NewRunArtifactResult(ctx, arc)
		if aerr != nil {
			clog.WarnContextf(ctx, "failed to create artifact result: %v", aerr)
		}
		result.Artifact = a
	}

	if err != nil {
		var rerr *docker.RunError
		if errors.As(err, &rerr) {
			if rerr.ExitCode == entrypoint.ProcessPausedCode {
				return result, nil
			}
			return result, fmt.Errorf("docker-on-host test failed: %w\n\n%s", err, stw.String())
		}
		return result, fmt.Errorf("docker-on-host test failed: %w\n\n%s", err, stw.String())
	}

	if err := d.stack.Add(func(ctx context.Context) error {
		return d.cli.Remove(ctx, &docker.Response{ID: cid})
	}); err != nil {
		return result, err
	}

	return result, nil
}
