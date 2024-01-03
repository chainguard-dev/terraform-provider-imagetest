package harnesses

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Executor is an interface for executing commands.
type Executor interface {
	Exec(ctx context.Context, command []string) (io.Reader, error)
}

// HostExecutor is an implementation of Executor that runs commands on the host
type HostExecutor struct {
	// Env is a map of environment variables to set when running commands.
	Env map[string]string
}

func NewHostExecutor() Executor {
	return &HostExecutor{
		Env: make(map[string]string),
	}
}

// Exec runs the given command using os/exec.
func (e *HostExecutor) Exec(ctx context.Context, command []string) (io.Reader, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)

	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out

	// Intentionally don't inherit the hosts environment variables to prevent
	// leaking things.
	env := make([]string, 0, len(e.Env))
	for k, v := range e.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running command: %w", err)
	}

	return out, nil
}
