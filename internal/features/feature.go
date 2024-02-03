package features

import (
	"context"
	"fmt"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ types.Feature = &feature{}

type feature struct {
	name        string
	description string
	steps       []types.Step
	labels      map[string]string
}

// Name implements types.Feature.
func (f *feature) Name() string {
	return f.name
}

// Steps implements types.Feature.
func (f *feature) Steps() []types.Step {
	return f.steps
}

// Labels implements types.Feature.
func (f *feature) Labels() map[string]string {
	return f.labels
}

type FeatureBuilder struct {
	feat *feature
}

func NewBuilder(name string) *FeatureBuilder {
	return &FeatureBuilder{
		feat: &feature{
			name:  name,
			steps: make([]types.Step, 0),
		},
	}
}

// Build the feature for the given environment.
func (b *FeatureBuilder) Build() types.Feature {
	return b.feat
}

func (b *FeatureBuilder) WithDescription(desc string) *FeatureBuilder {
	b.feat.description = desc
	return b
}

func (b *FeatureBuilder) WithStep(step types.Step) *FeatureBuilder {
	b.feat.steps = append(b.feat.steps, step)
	return b
}

type StepOpt func(*step)

func NewStep(name string, level types.Level, fn types.StepFn, opts ...StepOpt) types.Step {
	step := &step{
		name:  name,
		level: level,
		fn:    fn,
		backoff: wait.Backoff{
			// default to the equivalent of no retry
			Duration: 0 * time.Second,
			Steps:    1,
			Factor:   1.0,
		},
	}

	for _, opt := range opts {
		opt(step)
	}

	return step
}

func WithStepBackoff(backoff wait.Backoff) StepOpt {
	return func(s *step) {
		s.backoff = backoff
	}
}

type step struct {
	fn      types.StepFn
	name    string
	level   types.Level
	backoff wait.Backoff
}

// Fn implements types.Step.
func (s *step) Fn() types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		var rerr error
		var retries int

		err := wait.ExponentialBackoffWithContext(ctx, s.backoff, func(ctx context.Context) (bool, error) {
			retries++
			if _, err := s.fn(ctx); err != nil {
				if rerr != nil {
					rerr = fmt.Errorf("%w\n[%d/%d]: error %v", rerr, retries, s.backoff.Steps, err)
				} else {
					rerr = fmt.Errorf("[%d/%d]: error %v", retries, s.backoff.Steps, err)
				}
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return ctx, fmt.Errorf("step failed after %d retries:\n%w", retries, rerr)
		}
		return ctx, nil
	}
}

// Level implements types.Step.
func (s *step) Level() types.Level {
	return s.level
}

// Name implements types.Step.
func (s *step) Name() string {
	return s.name
}
