package harnesses

import (
	"context"
	"sync"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// nonidempotentBase is a non-idempotent base harness implementation. it can be
// embedded into other harnesses to provide the helper utilities for creating a
// non-idempotent harness (one where the Setup() function is not idempotent).
type nonidempotentBase struct {
	triggered chan struct{}
	once      sync.Once
	using     int
}

// Setup implements types.Harness.
func (h *nonidempotentBase) Setup() types.EnvFunc {
	return h.NonidempotentSetup(func(ctx context.Context, ec types.EnvConfig) (context.Context, error) {
		return ctx, nil
	})
}

func (h *nonidempotentBase) NonidempotentSetup(f types.EnvFunc) types.EnvFunc {
	return func(ctx context.Context, cfg types.EnvConfig) (context.Context, error) {
		h.using++

		var onceErr error
		h.once.Do(func() {
			tflog.Info(ctx, "Triggering base harness")
			close(h.triggered)

			if _, err := f(ctx, cfg); err != nil {
				onceErr = err
				return
			}
		})
		if onceErr != nil {
			return ctx, onceErr
		}

		return ctx, nil
	}
}

func (h *nonidempotentBase) Finish() types.EnvFunc {
	return func(ctx context.Context, _ types.EnvConfig) (context.Context, error) {
		h.using--
		tflog.Info(ctx, "Finished base harness...")

		h.once.Do(func() {
			tflog.Info(ctx, "Triggering base harness")
			// Close the channel if it's not already closed, this supports the use
			// case where the harness is not used, but still needs to be triggered
			// (such as during label filtering)
			close(h.triggered)
		})
		return ctx, nil
	}
}

func (h *nonidempotentBase) Finished(ctx context.Context) error {
	<-h.triggered

	for h.using > 0 {
		time.Sleep(1 * time.Second)
		tflog.Debug(ctx, "Waiting for all tests to finish...", map[string]interface{}{
			"test_counter": h.using,
		})
	}

	return nil
}

// Destroy implements types.Harness.
func (h *nonidempotentBase) Destroy(ctx context.Context) error {
	tflog.Info(ctx, "Destorying base harness...")
	return nil
}

func NewBase() *nonidempotentBase {
	return &nonidempotentBase{
		triggered: make(chan struct{}),
	}
}
