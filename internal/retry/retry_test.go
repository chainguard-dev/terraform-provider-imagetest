package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_Success(t *testing.T) {
	calls := 0
	result := Do(context.Background(), Config{Attempts: 3, Delay: time.Millisecond}, func(_ context.Context, attempt int) error {
		calls++
		return nil
	})

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if result.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", result.Attempts)
	}
	if result.Retried {
		t.Fatal("expected Retried=false")
	}
	if result.LastError != nil {
		t.Fatalf("expected nil LastError, got %v", result.LastError)
	}
}

func TestDo_SuccessAfterRetry(t *testing.T) {
	calls := 0
	result := Do(context.Background(), Config{Attempts: 3, Delay: time.Millisecond}, func(_ context.Context, attempt int) error {
		calls++
		if attempt < 3 {
			return errors.New("transient")
		}
		return nil
	})

	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	if result.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", result.Attempts)
	}
	if !result.Retried {
		t.Fatal("expected Retried=true")
	}
	if result.LastError != nil {
		t.Fatalf("expected nil LastError on success, got %v", result.LastError)
	}
}

func TestDo_AllAttemptsFail(t *testing.T) {
	calls := 0
	result := Do(context.Background(), Config{Attempts: 2, Delay: time.Millisecond}, func(_ context.Context, _ int) error {
		calls++
		return errors.New("permanent")
	})

	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
	if result.Attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", result.Attempts)
	}
	if !result.Retried {
		t.Fatal("expected Retried=true")
	}
	if result.LastError == nil || result.LastError.Error() != "permanent" {
		t.Fatalf("expected 'permanent' error, got %v", result.LastError)
	}
}

func TestDo_NoRetry(t *testing.T) {
	calls := 0
	result := Do(context.Background(), None(), func(_ context.Context, _ int) error {
		calls++
		return errors.New("fail")
	})

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if result.Retried {
		t.Fatal("expected Retried=false")
	}
}

func TestDo_RespectsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	calls := 0
	result := Do(ctx, Config{Attempts: 100, Delay: 50 * time.Millisecond}, func(_ context.Context, _ int) error {
		calls++
		return errors.New("fail")
	})

	// Should have stopped early due to context deadline during backoff
	if calls >= 100 {
		t.Fatalf("expected early termination, got %d calls", calls)
	}
	if result.LastError == nil {
		t.Fatal("expected non-nil LastError")
	}
}

func TestDo_ZeroAttemptsDefaultsToOne(t *testing.T) {
	calls := 0
	Do(context.Background(), Config{Attempts: 0}, func(_ context.Context, _ int) error {
		calls++
		return nil
	})

	if calls != 1 {
		t.Fatalf("expected 1 call with Attempts=0, got %d", calls)
	}
}

func TestApplyEnvOverride(t *testing.T) {
	t.Run("no env", func(t *testing.T) {
		cfg := applyEnvOverride(Config{Attempts: 5})
		if cfg.Attempts != 5 {
			t.Fatalf("expected 5, got %d", cfg.Attempts)
		}
	})

	t.Run("override to 1", func(t *testing.T) {
		t.Setenv("IMAGETEST_RETRY_ATTEMPTS", "0")
		cfg := applyEnvOverride(Config{Attempts: 5})
		if cfg.Attempts != 1 {
			t.Fatalf("expected 1 (clamped from 0), got %d", cfg.Attempts)
		}
	})

	t.Run("override to 10", func(t *testing.T) {
		t.Setenv("IMAGETEST_RETRY_ATTEMPTS", "10")
		cfg := applyEnvOverride(Config{Attempts: 3})
		if cfg.Attempts != 10 {
			t.Fatalf("expected 10, got %d", cfg.Attempts)
		}
	})

	t.Run("invalid value ignored", func(t *testing.T) {
		t.Setenv("IMAGETEST_RETRY_ATTEMPTS", "abc")
		cfg := applyEnvOverride(Config{Attempts: 3})
		if cfg.Attempts != 3 {
			t.Fatalf("expected 3 (unchanged), got %d", cfg.Attempts)
		}
	})
}
