package envs

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

type actions map[types.Level][]types.Step

func actionsFromSteps(step ...types.Step) actions {
	m := make(actions)
	for _, s := range step {
		m[s.Level()] = append(m[s.Level()], s)
	}
	return m
}

func (a actions) run(ctx context.Context) error {
	for _, setup := range a[types.Setup] {
		_ctx, err := setup.Fn()(ctx)
		if err != nil {
			return err
		}
		ctx = _ctx
	}

	for _, assessment := range a[types.Assessment] {
		_ctx, err := assessment.Fn()(ctx)
		if err != nil {
			return err
		}
		ctx = _ctx
	}

	for _, teardown := range a[types.Teardown] {
		_ctx, err := teardown.Fn()(ctx)
		if err != nil {
			return err
		}
		ctx = _ctx
	}

	return nil
}
