package environment

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

// environment is the default environment implementation.
type environment struct {
	labels Labels
}

type option func(*environment)

func New(opts ...option) types.Environment {
	env := &environment{
		labels: make(Labels),
	}

	for _, opt := range opts {
		opt(env)
	}

	return env
}

func WithLabels(labels Labels) option {
	return func(e *environment) {
		e.labels = labels
	}
}

func WithLabelsFromEnv(env string) option {
	return func(e *environment) {
		e.labels = make(Labels)
		for _, label := range strings.Split(env, ",") {
			parts := strings.SplitN(label, "=", 2)
			if len(parts) != 2 {
				continue
			}
			e.labels[parts[0]] = parts[1]
		}
	}
}

// Test implements types.Environment.
func (e *environment) Test(ctx context.Context, feature types.Feature) (err error) {
	// If the feature labels do not match the environment labels, skip the feature
	if e.labels != nil && !e.labels.Includes(feature.Labels()) {
		err = &ErrTestSkipped{
			FeatureName:   feature.Name(),
			FeatureLabels: feature.Labels(),
			CheckedLabels: e.labels,
		}
		return err
	}

	actions := make(map[types.Level][]types.Step)

	for _, s := range feature.Steps() {
		actions[s.Level()] = append(actions[s.Level()], s)
	}

	wraperr := func(e error) error {
		if err == nil {
			return e
		}
		return fmt.Errorf("%v; %w", err, e)
	}

	afters := func() {
		for _, after := range actions[types.After] {
			c, e := after.Fn()(ctx)
			if e != nil {
				err = wraperr(fmt.Errorf("during after step: %v", e))
			}
			ctx = c
		}
	}
	defer afters()

	for _, before := range actions[types.Before] {
		c, e := before.Fn()(ctx)
		if e != nil {
			return wraperr(fmt.Errorf("during before step: %v", e))
		}
		ctx = c
	}

	for _, assessment := range actions[types.Assessment] {
		c, e := assessment.Fn()(ctx)
		if e != nil {
			return wraperr(fmt.Errorf("during assessment step: %v", e))
		}
		ctx = c
	}

	return nil
}

type ErrTestSkipped struct {
	FeatureName   string
	FeatureLabels Labels
	CheckedLabels Labels
}

func (e *ErrTestSkipped) Error() string {
	return fmt.Sprintf("test %v skipped", e.FeatureName)
}

type Labels map[string]string

func (l Labels) Includes(got Labels) bool {
	if len(l) == 0 {
		return true
	}

	for k, v := range got {
		if l[k] == v {
			// return if any of the got labels match
			return true
		}
	}
	return false
}
