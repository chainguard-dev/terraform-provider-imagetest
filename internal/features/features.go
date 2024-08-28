package features

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"k8s.io/apimachinery/pkg/util/wait"
)

type Feature struct {
	Name        string
	Description string
	Labels      map[string]string

	befores     []*step
	afters      []*step
	assessments []*step
}

type step struct {
	Name string
	Fn   StepFn

	level Level
}

type StepOpt func(s *step)

// StepWithRetry wraps the step in an exponential backoff retry loop.
func StepWithRetry(backoff wait.Backoff) StepOpt {
	return func(s *step) {
		of := s.Fn
		s.Fn = func(ctx context.Context) error {
			var attempts int

			return wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
				attempts++
				err := of(ctx)
				if err != nil {
					log.Info(ctx, fmt.Sprintf("step failed attempt [%d/%d]", attempts, backoff.Steps), "name", s.Name, "error", err)
					return false, nil
				}
				log.Info(ctx, fmt.Sprintf("step succeeded attempt [%d/%d]", attempts, backoff.Steps), "name", s.Name)
				return true, nil
			})
		}
	}
}

type StepFn func(context.Context) error

type Level uint8

const (
	Before Level = iota
	Assessment
	After
)

type Option func(*Feature)

func New(name string, opts ...Option) *Feature {
	f := &Feature{
		Name:        name,
		Labels:      make(map[string]string),
		assessments: make([]*step, 0),
		befores:     make([]*step, 0),
		afters:      make([]*step, 0),
	}

	for _, opt := range opts {
		opt(f)
	}

	return f
}

func WithDescription(desc string) Option {
	return func(f *Feature) {
		f.Description = desc
	}
}

func (f *Feature) WithBefore(name string, fn StepFn, opts ...StepOpt) {
	f.withStep(name, fn, Before, opts...)
}

func (f *Feature) WithAfter(name string, fn StepFn, opts ...StepOpt) {
	f.withStep(name, fn, After, opts...)
}

func (f *Feature) WithAssessment(name string, fn StepFn, opts ...StepOpt) {
	f.withStep(name, fn, Assessment, opts...)
}

func (f *Feature) withStep(name string, fn StepFn, level Level, opts ...StepOpt) {
	s := &step{
		Name:  name,
		Fn:    fn,
		level: level,
	}
	for _, opt := range opts {
		opt(s)
	}
	switch level {
	case Before:
		f.befores = append(f.befores, s)
	case After:
		f.afters = append(f.afters, s)
	case Assessment:
		f.assessments = append(f.assessments, s)
	}
}

// Test executes the steps in the feature. The "before" steps are executed
// first, followed by the "assessments", followed by the "afters". On failures,
// the steps are short-circuited to the "afters". The "afters" are _always_
// run.
func (f *Feature) Test(ctx context.Context) error {
	var collectedError error

	collectError := func(err error) {
		if collectedError == nil {
			collectedError = err
		} else {
			collectedError = fmt.Errorf("%w; %v", collectedError, err)
		}
	}

	afters := func() {
		for _, after := range f.afters {
			if err := after.Fn(ctx); err != nil {
				collectError(fmt.Errorf("after step '%s' failed:\n%v", after.Name, err))
				// Don't continue if we error
				break
			}
		}
	}

	for _, before := range f.befores {
		if err := before.Fn(ctx); err != nil {
			collectError(fmt.Errorf("before step '%s' failed:\n%v", before.Name, err))
			afters()
			return collectedError
		}
	}

	for _, assessment := range f.assessments {
		if err := assessment.Fn(ctx); err != nil {
			collectError(fmt.Errorf("assessment step '%s' failed:\n%v", assessment.Name, err))
			afters()
			return collectedError
		}
	}

	afters()

	return collectedError
}
