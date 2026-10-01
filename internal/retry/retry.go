package retry

import (
	"context"
	"time"

	"github.com/chainguard-dev/clog"
)

// Config controls retry behavior.
type Config struct {
	// Attempts is the total number of attempts (including the initial one).
	// 1 means no retry.
	Attempts int

	// Delay is the wait duration between attempts.
	Delay time.Duration
}

// IsLast reports whether attempt (1-based) is the last one Do makes. Do may
// still stop earlier if its context ends.
func (c Config) IsLast(attempt int) bool {
	return attempt >= max(c.Attempts, 1)
}

// Result is returned by Do and contains the outcome of the retry loop.
type Result struct {
	// Attempts is the total number of attempts made.
	Attempts int

	// Retried is true if more than one attempt was made.
	Retried bool

	// LastError is the error from the final failed attempt, or nil on success.
	LastError error
}

// Do executes fn up to cfg.Attempts times, stopping on first success.
func Do(ctx context.Context, cfg Config, fn func(ctx context.Context, attempt int) error) Result {
	var lastErr error
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return Result{Attempts: attempt - 1, Retried: attempt > 2, LastError: lastErr}
		}

		if attempt > 1 {
			clog.WarnContext(ctx, "retrying",
				"attempt", attempt, "max", cfg.Attempts,
				"delay", cfg.Delay, "previous_error", lastErr)

			select {
			case <-time.After(cfg.Delay):
			case <-ctx.Done():
				return Result{Attempts: attempt - 1, Retried: true, LastError: lastErr}
			}
		}

		if err := fn(ctx, attempt); err != nil {
			lastErr = err
			if cfg.IsLast(attempt) {
				return Result{Attempts: attempt, Retried: attempt > 1, LastError: lastErr}
			}
			continue
		}

		return Result{Attempts: attempt, Retried: attempt > 1, LastError: lastErr}
	}
}
