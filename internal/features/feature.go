package features

import (
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

var _ types.Feature = &feature{}

type feature struct {
	name        string
	description string
	steps       []types.Step
}

// Name implements types.Feature.
func (f *feature) Name() string {
	return f.name
}

// Steps implements types.Feature.
func (f *feature) Steps() []types.Step {
	return f.steps
}

type FeatureBuilder struct {
	feat       *feature
	assertions []types.Assertion
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
func (b *FeatureBuilder) Build(env types.Environment) types.Feature {
	// convert the environment independent assertions into environment specific Steps

	for _, assertion := range b.assertions {
		b.feat.steps = append(b.feat.steps, env.Stepper(assertion))
	}

	return b.feat
}

func (b *FeatureBuilder) WithDescription(desc string) *FeatureBuilder {
	b.feat.description = desc
	return b
}

func (b *FeatureBuilder) WithAssertion(assertion ...types.Assertion) *FeatureBuilder {
	b.assertions = append(b.assertions, assertion...)
	return b
}
