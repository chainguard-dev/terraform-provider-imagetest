package types

import (
	"context"
)

type EnvConfig interface{}

type EnvFunc func(context.Context, EnvConfig) (context.Context, error)

// Environment defines an ephemeral testing environment capable of executing tests.
type Environment interface {
	// Test executes a feature(set) against the environment.
	Test(context.Context, ...Feature) error

	// Stepper returns an environment specific Step for the given assertion
	Stepper(Assertion) Step

	// Setup registers environment funcs to be executed before the
	// feature(set). The EnvFuncs are capable of being modified during the
	// setup.
	Setup(...EnvFunc) Environment

	// Finish signals that the environment is no longer needed and can be destroyed.
	Finish(...EnvFunc) Environment

	// Run starts the environment. Executing the setup funcs and deferring the
	// finish funcs. Tests can only be ran once the environment is running.
	Run(context.Context) (func() error, error)
}

// Harness defines the methods used by environments to create and destroy the environment specific test harness.
type Harness interface {
	// Setup returns the EnvFunc that creates the harness, and modifies the
	// environment config as necessary. When not idempotent, the Setup function
	// will ensure it is only called once per Harness.
	Setup() EnvFunc

	// Finish returns the EnvFunc ran by the Environment to signal that it is
	// done with the harness. It is _not_ the same as Destroy.
	Finish() EnvFunc

	// Destroy destroys the harness. It blocks on the harness being finished by
	// all environments.
	Destroy(context.Context) error

	Finished(context.Context) error
}

type Feature interface {
	Name() string
	Steps() []Step
}

type Level uint8

const (
	Setup Level = iota
	Assessment
	Teardown
)

type StepFn func(ctx context.Context) (context.Context, error)

type Step interface {
	Name() string
	Fn() StepFn
	Level() Level
}

type Assertion interface {
	Name() string
	Command(context.Context) string
	Level() Level
}
