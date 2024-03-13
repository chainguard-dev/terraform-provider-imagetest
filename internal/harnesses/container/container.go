package container

import (
	"context"
	"fmt"
	"io"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ types.Harness = &container{}

// container is a harness that spins up a container and steps within the
// container environment.
type container struct {
	*base.Base
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
func (h *container) StepFn(config types.StepConfig) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		log.Info(ctx, "stepping in container", "command", config.Command)
		r, err := h.provider.Exec(ctx, provider.ExecConfig{
			Command:    config.Command,
			WorkingDir: config.WorkingDir,
		})
		if err != nil {
			return ctx, fmt.Errorf("failed to execute command: %w", err)
		}

		out, err := io.ReadAll(r)
		if err != nil {
			return ctx, err
		}
		log.Info(ctx, "finished stepping in container", "command", config.Command, "out", string(out))

		return ctx, nil
	}
}

type Config struct {
	Env            map[string]string
	Ref            name.Reference
	Mounts         []ConfigMount
	ManagedVolumes []ConfigMount
	Networks       []string
	Privileged     bool
}

// ConfigMount is a simplified wrapper around mount.Mount.
type ConfigMount struct {
	Type        mount.Type
	Source      string
	Destination string
}

func New(name string, cli *provider.DockerClient, cfg Config) (types.Harness, error) {
	// TODO: Support more providers

	mounts := make([]mount.Mount, 0, len(cfg.Mounts))
	for _, m := range cfg.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:   m.Type,
			Source: m.Source,
			Target: m.Destination,
		})
	}

	managedVolumes := make([]mount.Mount, 0, len(cfg.ManagedVolumes))
	for _, m := range cfg.ManagedVolumes {
		managedVolumes = append(managedVolumes, mount.Mount{
			Type:   m.Type,
			Source: m.Source,
			Target: m.Destination,
		})
	}

	p := provider.NewDocker(name, cli, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Ref:        cfg.Ref,
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{"tail -f /dev/null"},
			Env:        cfg.Env,
			Networks:   cfg.Networks,
			Resources: provider.ContainerResourcesRequest{
				// Default to something small just for "scheduling" purposes
				CpuRequest:    resource.MustParse("100m"),
				MemoryRequest: resource.MustParse("250Mi"),
			},
		},
		Mounts:         mounts,
		ManagedVolumes: managedVolumes,
	})

	return &container{
		Base:     base.New(),
		provider: p,
	}, nil
}
