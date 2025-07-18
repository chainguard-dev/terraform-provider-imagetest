package mock

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

func NewWaiter() Waiter {
	return Waiter{
		active: new(atomic.Int32),
	}
}

// Waiter is like a 'sync.WaitGroup', save that its 'Wait' function accepts a
// 'context.Context' and supports deadlines.
type Waiter struct {
	active *atomic.Int32
}

func (self Waiter) Add() {
	self.active.Add(1)
}

func (self Waiter) Done() {
	left := self.active.Add(-1)
	_, file, line, _ := runtime.Caller(1)
	i := strings.LastIndexByte(file, '/')
	file = file[i+1:]
	caller := fmt.Sprintf("%s:%d", file, line)
	log.Debug("waiter.Done() called", "caller", caller, "left", left)
}

func (self Waiter) WaitContext(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return context.DeadlineExceeded
		case <-time.After(1 * time.Millisecond):
			if self.active.Load() == 0 {
				return nil
			}
		}
	}
}
