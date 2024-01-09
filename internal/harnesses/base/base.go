package base

import (
	"context"
	"sync"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

// Base is a Base harness implementation. It's often useful to embed this into
// other harness implementations.
type Base struct {
	triggered chan struct{}
	using     int
	once      sync.Once
	mu        sync.Mutex
}

func New() *Base {
	return &Base{
		mu:        sync.Mutex{},
		triggered: make(chan struct{}),
	}
}

func (h *Base) WithCreate(f types.StepFn) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		h.using++

		// Lock to ensure concurrent calls block until the once.Do() is complete.
		// This means the concurrent calls to WithCreate() block until the first
		// call to WithCreate() is complete (the harness is up). Use a defer so
		// failures in the once.Do() do not deadlock.
		h.mu.Lock()
		defer h.mu.Unlock()

		var onceErr error
		h.once.Do(func() {
			close(h.triggered)

			if _, err := f(ctx); err != nil {
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

func (h *Base) Finish() types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		h.using--

		h.once.Do(func() {
			close(h.triggered)
		})

		return ctx, nil
	}
}

func (h *Base) Done() error {
	<-h.triggered
	for h.using > 0 {
		time.Sleep(1 * time.Second)
	}
	return nil
}
