package dockerindocker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
)

type driver struct {
	ImageRef name.Reference // The image to use for docker-in-docker

	name   string
	stack  *harness.Stack
	cli    *docker.Client
	resp   *docker.Response
	config *dockerConfig
	ropts  []remote.Option
}

func NewDriver(n string, opts ...DriverOpts) (drivers.Tester, error) {
	d := &driver{
		ImageRef: name.MustParseReference("cgr.dev/chainguard-private/docker-dind:latest"),
		name:     n,
		stack:    harness.NewStack(),
		ropts: []remote.Option{
			remote.WithPlatform(ggcrv1.Platform{
				OS:           "linux",
				Architecture: runtime.GOARCH,
			}),
		},
		config: &dockerConfig{},
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
func (d *driver) Run(ctx context.Context, ref name.Reference) error {
	fmt.Println("building dind image")
	dind, err := remote.Image(d.ImageRef, d.ropts...)
	if err != nil {
		return err
	}

	timg, err := remote.Image(ref, d.ropts...)
	if err != nil {
		return err
	}

	layers, err := timg.Layers()
	if err != nil {
		return fmt.Errorf("failed to get layers: %w", err)
	}

	for _, l := range layers {
		dind, err = mutate.AppendLayers(dind, l)
		if err != nil {
			return err
		}
	}

	dindcf, err := dind.ConfigFile()
	if err != nil {
		return err
	}

	timgcf, err := timg.ConfigFile()
	if err != nil {
		return err
	}

	// Merge the two environment vars, dind takes precedence
	dindcf.Config.Env = append(dindcf.Config.Env, timgcf.Config.Env...)

	dind, err = mutate.ConfigFile(dind, dindcf)
	if err != nil {
		return err
	}

	ddig, err := dind.Digest()
	if err != nil {
		return err
	}

	r := ref.Context().Digest(ddig.String())

	if err := remote.Write(r, dind, d.ropts...); err != nil {
		return err
	}

	nw, err := d.cli.CreateNetwork(ctx, &docker.NetworkRequest{})
	if err != nil {
		return err
	}

	if err := d.stack.Add(func(ctx context.Context) error {
		return d.cli.RemoveNetwork(ctx, nw)
	}); err != nil {
		return err
	}

	content := []*docker.Content{}
	cfg, err := d.config.Content()
	if err != nil {
		return err
	}
	content = append(content, cfg)

	resp, err := d.cli.Start(ctx, &docker.Request{
		Name:       d.name,
		Ref:        r,
		Privileged: true,
		User:       "0:0",
		Networks: []docker.NetworkAttachment{{
			Name: nw.Name,
			ID:   nw.ID,
		}},
		HealthCheck: &v1.HealthcheckConfig{
			Test:        []string{"CMD", "/bin/sh", "-c", "docker info"},
			Interval:    2 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     10,
			StartPeriod: 1 * time.Second,
		},
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Contents:   content,
	})
	if err != nil {
		return err
	}
	d.resp = resp

	if err := d.stack.Add(func(ctx context.Context) error {
		return d.cli.Remove(ctx, resp)
	}); err != nil {
		return err
	}

	return d.resp.Run(ctx, harness.Command{
		// TODO: This is dumb, replace this when we have our own entrypoint runner
		Args:   "bash -eux -o pipefail -c 'for script in $(find /imagetest -maxdepth 1 -name \"*.sh\" | sort); do echo \"Executing $script\"; chmod +x \"$script\"; source \"$script\"; done'",
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
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
