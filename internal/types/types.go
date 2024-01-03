package types

import (
	"context"
)

const ProviderName = "imagetest"

type Environment interface {
	// Test executes a feature(set) against the environment.
	Test(context.Context, Feature) error
}

type Harness interface {
	// Setup returns the Step that creates the harness and signals the caller is
	// using the harness.
	Setup() StepFn

	// Finish returns the StepFn that signals the caller is done with the
	// harness.
	Finish() StepFn

	// Destroy destroys the harness.
	Destroy(context.Context) error

	// Done blocks until all callers of the harness are done with it.
	Done() error

	// StepFn returns a StepFn that executes the given command in the harness.
	StepFn(command string) StepFn
}

type Feature interface {
	Name() string
	Labels() map[string]string
	Steps() []Step
}

type Level uint8

const (
	Before Level = iota
	Assessment
	After
)

type StepFn func(ctx context.Context) (context.Context, error)

type Step interface {
	Name() string
	Fn() StepFn
	Level() Level
}
