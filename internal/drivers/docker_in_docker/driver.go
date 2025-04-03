package dockerindocker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/google/go-containerregistry/pkg/name"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
)

type driver struct {
	ImageRef   name.Reference    // The image to use for docker-in-docker
	Envs       map[string]string // Additional environment variables to set in the sandbox
	ExtraHosts []string          // Extra hosts (--add-hosts) to add to the sandbox
	Mirrors    []string          // Registry mirrors to use for docker-in-docker

	name      string
	stack     *harness.Stack
	cli       *docker.Client
	cliCfg    *dockerConfig
	daemonCfg *daemonConfig
	ropts     []remote.Option
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
		cliCfg: &dockerConfig{},
		daemonCfg: &daemonConfig{
			// DefaultAddressPool needs to be RFC 1918 compliant that doesn't overlap with the default dockerd's pool (172.17.0.0/16)
			DefaultAddressPool: "base=172.30.0.0/16,size=24",
		},
	}

	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}

	return d, nil
}

// Setup implements drivers.TestDriver.
func (d *driver) Setup(ctx context.Context) error {
	cli, err := docker.New()
	if err != nil {
		return err
	}
	d.cli = cli

	return nil
}

// Teardown implements drivers.TestDriver.
func (d *driver) Teardown(ctx context.Context) error {
	return d.stack.Teardown(ctx)
}

// Run implements drivers.TestDriver.
func (d *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	// Build the driver image, uses the provided dind image appended with the ref
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

				// Ensure we preserve things we want from the original image
				mcfgf.Config.Entrypoint = tcfgf.Config.Entrypoint
				mcfgf.Config.Cmd = tcfgf.Config.Cmd
				mcfgf.Config.WorkingDir = tcfgf.Config.WorkingDir

				// Append any environment vars
				mcfgf.Config.Env = append(mcfgf.Config.Env, tcfgf.Config.Env...)

				return mutate.ConfigFile(mutated, mcfgf)
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build driver image: %w", err)
	}

	nw, err := d.cli.CreateNetwork(ctx, &docker.NetworkRequest{})
	if err != nil {
		return nil, err
	}

	if err := d.stack.Add(func(ctx context.Context) error {
		return d.cli.RemoveNetwork(ctx, nw)
	}); err != nil {
		return nil, err
	}

	cliCfg, err := d.cliCfg.Content()
	if err != nil {
		return nil, err
	}

	daemonCfg, err := d.daemonCfg.Content()
	if err != nil {
		return nil, err
	}

	content := []*docker.Content{cliCfg, daemonCfg}

	r, w := io.Pipe()
	defer w.Close()

	// collect container output for better error messages
	stw := bytes.NewBuffer(nil)
	mw := io.MultiWriter(w, stw)

	go func() {
		defer r.Close()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			clog.InfoContext(ctx, "received container log line", drivers.LogAttributeKey, scanner.Text())
		}
	}()

	extraHosts := []string{"host.docker.internal:host-gateway"}
	extraHosts = append(extraHosts, d.ExtraHosts...)

	envs := []string{}
	for k, v := range d.Envs {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}

	clog.InfoContext(ctx, "running docker-in-docker test", "image_ref", tref.String())
	cid, err := d.cli.Run(ctx, &docker.Request{
		Name:       d.name,
		Ref:        tref,
		Privileged: true, // Required for dind
		User:       "0:0",
		Networks: []docker.NetworkAttachment{{
			Name: nw.Name,
			ID:   nw.ID,
		}},
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
		Contents:   content,
		Logger:     mw,
	})

	result := &drivers.RunResult{}

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
			return result, fmt.Errorf("docker-in-docker test failed: %w\n\n%s", err, stw.String())
		}
		return result, fmt.Errorf("docker-in-docker test failed: %w\n\n%s", err, stw.String())
	}

	if err := d.stack.Add(func(ctx context.Context) error {
		return d.cli.Remove(ctx, &docker.Response{
			ID: cid,
		})
	}); err != nil {
		return result, err
	}

	return result, nil
}

type dockerConfig struct {
	Auths map[string]dockerAuthEntry `json:"auths,omitempty"`
}

type dockerAuthEntry struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

func (c dockerConfig) Content() (*docker.Content, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}

	return docker.NewContentFromString(string(data), "/root/.docker/config.json"), nil
}

type daemonConfig struct {
	Mirrors            []string `json:"registry-mirrors,omitempty"`
	DefaultAddressPool string   `json:"default-address-pool,omitempty"`
}

func (c daemonConfig) Content() (*docker.Content, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}

	return docker.NewContentFromString(string(data), "/etc/docker/daemon.json"), nil
}
