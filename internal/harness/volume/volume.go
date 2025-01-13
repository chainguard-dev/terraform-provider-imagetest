package volume

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
)

var _ harness.Harness = &volume{}

type volume struct {
	Name   string
	Labels map[string]string

	stack *harness.Stack
}

func New(opts ...Option) harness.Harness {
	v := &volume{
		Labels: make(map[string]string),
		stack:  harness.NewStack(),
	}

	for _, opt := range opts {
		opt(v)
	}

	return v
}

func (v *volume) Run(context.Context, name.Reference) error {
	panic("implement me")
}

// Create implements harness.Harness.
func (v *volume) Create(ctx context.Context) error {
	cli, err := docker.New()
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}

	m, err := cli.CreateVolume(ctx, &docker.VolumeRequest{
		Name:   v.Name,
		Labels: v.Labels,
	})
	if err != nil {
		return fmt.Errorf("creating volume: %w", err)
	}

	if err := v.stack.Add(func(ctx context.Context) error {
		return cli.RemoveVolume(ctx, m)
	}); err != nil {
		return fmt.Errorf("adding volume teardown to stack: %w", err)
	}

	return nil
}

// Destroy implements harness.Harness.
func (v *volume) Destroy(ctx context.Context) error {
	return v.stack.Teardown(ctx)
}

// Exec implements harness.Harness.
func (v *volume) Exec(context.Context, harness.Command) error {
	// "running" a volume is a no-op
	return nil
}

type Option func(*volume)

func WithName(name string) Option {
	return func(v *volume) {
		v.Name = name
	}
}

func WithLabels(labels map[string]string) Option {
	return func(v *volume) {
		if labels == nil {
			labels = make(map[string]string)
		}
		v.Labels = labels
	}
}
