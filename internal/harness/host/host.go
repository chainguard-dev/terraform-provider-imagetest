package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
)

var _ harness.Harness = &host{}

// host is a harness type that runs steps on the host machine.
type host struct {
	env map[string]string
}

func NewHost() harness.Harness {
	return &host{
		env: make(map[string]string),
	}
}

// Create implements harness.Harness.
func (h *host) Create(context.Context) error {
	return nil
}

// Run implements harness.Harness.
func (h *host) Run(ctx context.Context, cmd harness.Command) error {
	if _, err := h.exec(ctx, []string{"sh", "-c", cmd.Args}); err != nil {
		return fmt.Errorf("running step on host: %w", err)
	}
	return nil
}

// Destroy implements types.Harn.
func (*host) Destroy(context.Context) error {
	return nil
}

func (h *host) DebugLogCommand() string {
	// TODO implement something here
	return ""
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
