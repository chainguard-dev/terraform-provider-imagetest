package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

var _ types.Harness = &host{}

// host is a harness type that runs steps on the host machine.
type host struct {
	*base.Base

	env map[string]string
}

func (h *host) DebugLogCommand() string {
	// TODO implement something here
	return ""
}

func NewHost() types.Harness {
	return &host{
		Base: base.New(),
		env:  make(map[string]string),
	}
}

// StepFn implements types.Harness.
func (h *host) StepFn(config types.StepConfig) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		if _, err := h.exec(ctx, []string{"sh", "-c", config.Command}); err != nil {
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

func (h *host) exec(ctx context.Context, command []string) (io.Reader, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)

	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out

	// Intentionally don't inherit the hosts environment variables to prevent
	// leaking things.
	env := make([]string, 0, len(h.env))
	for k, v := range h.env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running command: %w", err)
	}

	return out, nil
}
