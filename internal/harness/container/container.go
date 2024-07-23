package container

import (
	"context"
	"fmt"
	"io"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ harness.Harness = &container{}

// container is a harness that spins up a container and steps within the
// container environment.
type container struct {
	provider provider.Provider
}

// Create implements harness.Harness.
func (h *container) Create(ctx context.Context) error {
	return h.provider.Start(ctx)
}

// Run implements harness.Harness.
func (h *container) Run(ctx context.Context, cmd harness.Command) error {
	log.Info(ctx, "stepping in container", "command", cmd.Args)
	r, err := h.provider.Exec(ctx, provider.ExecConfig{
		Command:    cmd.Args,
		WorkingDir: cmd.WorkingDir,
	})
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	log.Info(ctx, "finished stepping in container", "command", cmd.Args, "out", string(out))

	return nil
}

func (h *container) DebugLogCommand() string {
	// TODO implement something here
	return ""
}

// Destroy implements types.Harness.
func (h *container) Destroy(ctx context.Context) error {
	return h.provider.Teardown(ctx)
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

func New(name string, cli *provider.DockerClient, cfg Config) harness.Harness {
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
			VolumeOptions: &mount.VolumeOptions{
				Labels: provider.DefaultLabels(),
			},
		})
	}

	p := provider.NewDocker(name, cli, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Ref:        cfg.Ref,
			Entrypoint: harness.DefaultEntrypoint(),
			Cmd:        harness.DefaultCmd(),
			Env:        cfg.Env,
			Networks:   cfg.Networks,
			Resources: provider.ContainerResourcesRequest{
				// Default to something small just for "scheduling" purposes
				CpuRequest:    resource.MustParse("100m"),
				MemoryRequest: resource.MustParse("250Mi"),
			},
			Privileged: cfg.Privileged,
			Labels:     provider.MainHarnessLabel(),
		},
		Mounts:         mounts,
		ManagedVolumes: managedVolumes,
	})

	return &container{
		provider: p,
	}
}
