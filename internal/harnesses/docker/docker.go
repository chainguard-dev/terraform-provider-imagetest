package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/mount"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

var _ types.Harness = &docker{}

const DefaultDockerSocketPath = "/var/run/docker.sock"

type docker struct {
	*base.Base
	id string

	container provider.Provider
}

func New(id string, cli *provider.DockerClient, opts ...Option) (types.Harness, error) {
	options := &HarnessDockerOptions{}
	for _, opt := range opts {
		if err := opt(options); err != nil {
			return nil, err
		}
	}

	dockerSocketPath := DefaultDockerSocketPath
	if options.SocketPath != "" {
		dockerSocketPath = options.SocketPath
	}

	var managedVolumes []mount.Mount
	for _, vol := range options.ManagedVolumes {
		managedVolumes = append(managedVolumes, mount.Mount{
			Type:   mount.TypeVolume,
			Source: vol.Source,
			Target: vol.Destination,
		})
	}

	var mounts []mount.Mount
	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: dockerSocketPath,
		Target: DefaultDockerSocketPath,
	})

	for _, mt := range options.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: mt.Source,
			Target: mt.Destination,
		})
	}

	container := provider.NewDocker(id, cli, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Ref:        options.ImageRef,
			Entrypoint: base.DefaultEntrypoint(),
			Cmd:        base.DefaultCmd(),
			Networks:   options.Networks,
			Env:        options.Envs,
		},
		Mounts:         mounts,
		ManagedVolumes: managedVolumes,
	})

	return &docker{
		Base:      base.New(),
		id:        id,
		container: container,
	}, nil
}

func (h *docker) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		if err := h.container.Start(ctx); err != nil {
			return ctx, fmt.Errorf("failed starting docker service: %w", err)
		}

		if _, err := h.container.Exec(ctx, provider.ExecConfig{
			Command: "apk add docker-cli",
		}); err != nil {
			return ctx, fmt.Errorf("failed finishing setup: %w", err)
		}

		return ctx, nil
	})
}

func (h *docker) Destroy(ctx context.Context) error {
	if err := h.container.Teardown(ctx); err != nil {
		return fmt.Errorf("tearing down sandbox: %w", err)
	}

	return nil
}

func (h *docker) StepFn(config types.StepConfig) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		log.Info(ctx, "stepping in docker container", "command", config.Command)
		r, err := h.container.Exec(ctx, provider.ExecConfig{
			Command:    config.Command,
			WorkingDir: config.WorkingDir,
		})
		if err != nil {
			return ctx, err
		}

		out, err := io.ReadAll(r)
		if err != nil {
			return ctx, err
		}

		log.Info(ctx, "finished stepping in docker container", "command", config.Command, "out", string(out))

		return ctx, nil
	}
}
