package harnesses

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

var _ types.Harness = &host{}

// host is a harness type that runs steps on the host machine
type host struct {
	*base

	execer HostExecutor
}

func NewHost() types.Harness {
	return &host{
		base: NewBase(),
	}
}

// StepFn implements types.Harn.
func (h *host) StepFn(command string) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		if _, err := h.execer.Exec(ctx, []string{"sh", "-c", command}); err != nil {
			return ctx, fmt.Errorf("running step on host: %w", err)
		}
		return ctx, nil
	}
}

// Setup implements types.Harn.
func (h *host) Setup() types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		return ctx, nil
	}
}

// Destroy implements types.Harn.
func (*host) Destroy(context.Context) error {
	return nil
}
