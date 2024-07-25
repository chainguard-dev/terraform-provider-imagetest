package harness

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type Harness interface {
	Create(context.Context) error
	Destroy(context.Context) error
	Run(context.Context, Command) error
}

type Command struct {
	Args       string
	WorkingDir string
	Env        map[string]string
	Stdout     io.Writer
	Stderr     io.Writer
}

func DefaultEntrypoint() []string {
	return []string{"/bin/sh", "-c"}
}

func DefaultCmd() []string {
	return []string{"tail -f /dev/null"}
}

// Stack is a lifo queue used to easily manage resources that need to be torn
// down by harnesses. It is a LIFO queue, so the first item added is the last
// item torn down.
type Stack struct {
	mu    sync.Mutex
	stack []func(context.Context) error
	done  chan struct{}
}

func NewStack() *Stack {
	return &Stack{
		stack: make([]func(context.Context) error, 0),
		done:  make(chan struct{}),
		mu:    sync.Mutex{},
	}
}

func (r *Stack) Add(f func(ctx context.Context) error) error {
	select {
	case <-r.done:
		return fmt.Errorf("teardown already done")
	default:
		r.mu.Lock()
		defer r.mu.Unlock()

		r.stack = append(r.stack, f)
		return nil
	}
}

func (r *Stack) Teardown(ctx context.Context) error {
	r.mu.Lock()
	select {
	case <-ctx.Done():
		r.mu.Unlock()
		return ctx.Err()
	case <-r.done:
		r.mu.Unlock()
		return fmt.Errorf("teardown already done")
	default:
		close(r.done)
		r.mu.Unlock()
	}

	var errs []error
	for i := len(r.stack) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := r.stack[i](ctx); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to tear down resources: %v", errs)
	}

	return nil
}
