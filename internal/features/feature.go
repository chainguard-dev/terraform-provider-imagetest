package features

import (
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
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

// Build the feature for the given environment
func (b *FeatureBuilder) Build() types.Feature {
	return b.feat
}

func (b *FeatureBuilder) WithDescription(desc string) *FeatureBuilder {
	b.feat.description = desc
	return b
}

func (b *FeatureBuilder) WithBefore(name string, fn types.StepFn) *FeatureBuilder {
	b.feat.steps = append(b.feat.steps, newStep(name, types.Before, fn))
	return b
}

func (b *FeatureBuilder) WithAfter(name string, fn types.StepFn) *FeatureBuilder {
	b.feat.steps = append(b.feat.steps, newStep(name, types.After, fn))
	return b
}

func (b *FeatureBuilder) WithAssessment(name string, fn types.StepFn) *FeatureBuilder {
	b.feat.steps = append(b.feat.steps, newStep(name, types.Assessment, fn))
	return b
}

func (b *FeatureBuilder) WithLabels(labels map[string]string) *FeatureBuilder {
	b.feat.labels = labels
	return b
}

func (b *FeatureBuilder) WithStep(step types.Step) *FeatureBuilder {
	b.feat.steps = append(b.feat.steps, step)
	return b
}

type step struct {
	fn    types.StepFn
	name  string
	level types.Level
}

func newStep(name string, level types.Level, fn types.StepFn) *step {
	return &step{
		name:  name,
		level: level,
		fn:    fn,
	}
}

// Fn implements types.Step.
func (s *step) Fn() types.StepFn {
	return s.fn
}

// Level implements types.Step.
func (s *step) Level() types.Level {
	return s.level
}

// Name implements types.Step.
func (s *step) Name() string {
	return s.name
}
