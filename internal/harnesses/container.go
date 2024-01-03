package harnesses

import (
	"context"
	"io"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ types.Harness = &container{}

// container is a harness that spins up a container and steps within the
// container environment
type container struct {
	*base
	provider provider.Provider
}

// Setup implements types.Harness.
func (h *container) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		if err := h.provider.Start(ctx); err != nil {
			return ctx, err
		}

		return ctx, nil
	})
}

// Destroy implements types.Harness.
func (h *container) Destroy(ctx context.Context) error {
	return h.provider.Teardown(ctx)
}

// StepFn implements types.Harness.
func (h *container) StepFn(command string) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		r, err := h.provider.Exec(ctx, command)
		if err != nil {
			return ctx, err
		}

		out, err := io.ReadAll(r)
		if err != nil {
			return ctx, err
		}

		tflog.Info(ctx, "Executed container step", map[string]interface{}{
			"out":     string(out),
			"command": command,
		})

		return ctx, nil
	}
}

type ContainerConfig struct {
	Env        map[string]string
	Image      string
	Mounts     []ContainerConfigMount
	Privileged bool
}

// ContainerConfigMount is a simplified wrapper around mount.Mount, intended to
// only support BindMounts
type ContainerConfigMount struct {
	Source      string
	Destination string
}

func NewContainer(ctx context.Context, name string, cfg ContainerConfig) (types.Harness, error) {
	// TODO: Support more providers

	mounts := make([]mount.Mount, 0, len(cfg.Mounts))
	for _, m := range cfg.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: m.Source,
			Target: m.Destination,
		})
	}

	p, err := provider.NewDocker(name, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Image:      cfg.Image,
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{"tail -f /dev/null"},
			Env:        cfg.Env,
		},
		Mounts: mounts,
	})
	if err != nil {
		return nil, err
	}

	return &container{
		base:     NewBase(),
		provider: p,
	}, nil
}
