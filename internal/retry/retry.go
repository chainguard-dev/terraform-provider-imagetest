package retry

import (
	"context"
	"os"
	"strconv"
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

// None returns a Config that disables retry.
func None() Config {
	return Config{Attempts: 1}
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
// If IMAGETEST_RETRY_ATTEMPTS is set, it overrides cfg.Attempts (0 disables retry).
func Do(ctx context.Context, cfg Config, fn func(ctx context.Context, attempt int) error) Result {
	cfg = applyEnvOverride(cfg)

	if cfg.Attempts < 1 {
		cfg.Attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= cfg.Attempts; attempt++ {
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
			continue
		}

		return Result{Attempts: attempt, Retried: attempt > 1}
	}

	return Result{Attempts: cfg.Attempts, Retried: cfg.Attempts > 1, LastError: lastErr}
}

func applyEnvOverride(cfg Config) Config {
	v := os.Getenv("IMAGETEST_RETRY_ATTEMPTS")
	if v == "" {
		return cfg
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return cfg
	}
	if n <= 0 {
		n = 1
	}
	cfg.Attempts = n
	return cfg
}
