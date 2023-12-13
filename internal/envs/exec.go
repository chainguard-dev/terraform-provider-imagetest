package envs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ types.Environment = &execEnv{}

type ExecEnvConfig struct {
	Envs map[string]string
}

func NewExec() types.Environment {
	return &execEnv{
		Config: &ExecEnvConfig{
			Envs: make(map[string]string),
		},
		setup:    make([]types.EnvFunc, 0),
		teardown: make([]types.EnvFunc, 0),
	}
}

// execEnv is an environment implementation that runs on the host using cmd.Exec()
type execEnv struct {
	Config *ExecEnvConfig

	setup    []types.EnvFunc
	teardown []types.EnvFunc
}

func (e *execEnv) Stepper(assertion types.Assertion) types.Step {
	return &execStep{
		level:     assertion.Level(),
		name:      assertion.Name(),
		assertion: assertion,
		env:       e.Config.Envs,
	}
}

func (e *execEnv) Run(ctx context.Context) (func() error, error) {
	for _, setup := range e.setup {
		_, err := setup(ctx, e.Config)
		if err != nil {
			return nil, err
		}
	}

	return func() error {
		for _, teardown := range e.teardown {
			_, err := teardown(ctx, e.Config)
			if err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func (e *execEnv) Setup(funcs ...types.EnvFunc) types.Environment {
	e.setup = append(e.setup, funcs...)
	return e
}

// Finish implements types.Environment.
func (e *execEnv) Finish(funcs ...types.EnvFunc) types.Environment {
	e.teardown = append(e.teardown, funcs...)
	return e
}

// Test implements types.Environment.
func (e *execEnv) Test(ctx context.Context, feature ...types.Feature) error {
	for _, f := range feature {
		if err := actionsFromSteps(f.Steps()...).run(ctx); err != nil {
			return fmt.Errorf("testing feature '%s': %w", f.Name(), err)
		}
	}

	return nil
}

var _ types.Step = &execStep{}

type execStep struct {
	// env holds the environment variables to be set in the execution cmd context
	env map[string]string

	assertion types.Assertion
	name      string
	level     types.Level
}

// Fn implements types.Step.
func (s *execStep) Fn() types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		var bufout, buferr bytes.Buffer

		command := s.assertion.Command(ctx)

		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Stdout = &bufout
		cmd.Stderr = &buferr

		cmd.Env = os.Environ()
		for k, v := range s.env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}

		tflog.Info(ctx, "exec environment stepping...", map[string]interface{}{
			"step_name": s.name,
			"command":   cmd.String(),
			"stdout":    bufout.String(),
			"stderr":    buferr.String(),
		})

		if err := cmd.Run(); err != nil {
			// TODO: Structure this with custom error types
			return ctx, fmt.Errorf("running command: %w\n\n%v\n%v", err, cmd.String(), buferr.String())
		}

		return ctx, nil
	}
}

// Level implements types.Step.
func (s *execStep) Level() types.Level {
	return s.level
}

// Name implements types.Step.
func (s *execStep) Name() string {
	return s.name
}
