package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		// failUntil makes the fn return an error for attempts < failUntil
		failUntil    int
		wantCalls    int
		wantAttempts int
		wantRetried  bool
		wantErr      string // empty means nil
	}{
		{
			name:         "succeeds immediately",
			cfg:          Config{Attempts: 3, Delay: time.Millisecond},
			failUntil:    0,
			wantCalls:    1,
			wantAttempts: 1,
			wantRetried:  false,
		},
		{
			name:         "succeeds after retry",
			cfg:          Config{Attempts: 3, Delay: time.Millisecond},
			failUntil:    3,
			wantCalls:    3,
			wantAttempts: 3,
			wantRetried:  true,
			wantErr:      "fail",
		},
		{
			name:         "all attempts fail",
			cfg:          Config{Attempts: 2, Delay: time.Millisecond},
			failUntil:    999,
			wantCalls:    2,
			wantAttempts: 2,
			wantRetried:  true,
			wantErr:      "fail",
		},
		{
			name:         "no retry configured",
			cfg:          Config{},
			failUntil:    999,
			wantCalls:    1,
			wantAttempts: 1,
			wantRetried:  false,
			wantErr:      "fail",
		},
		{
			name:         "zero attempts defaults to one",
			cfg:          Config{Attempts: 0},
			failUntil:    0,
			wantCalls:    1,
			wantAttempts: 1,
			wantRetried:  false,
		},
		{
			name:         "negative attempts defaults to one",
			cfg:          Config{Attempts: -5},
			failUntil:    0,
			wantCalls:    1,
			wantAttempts: 1,
			wantRetried:  false,
		},
		{
			name:         "single attempt failure",
			cfg:          Config{Attempts: 1},
			failUntil:    999,
			wantCalls:    1,
			wantAttempts: 1,
			wantRetried:  false,
			wantErr:      "fail",
		},
		{
			name:         "zero delay retries immediately",
			cfg:          Config{Attempts: 3, Delay: 0},
			failUntil:    3,
			wantCalls:    3,
			wantAttempts: 3,
			wantRetried:  true,
			wantErr:      "fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			result := Do(context.Background(), tt.cfg, func(_ context.Context, attempt int) error {
				calls++
				if attempt < tt.failUntil {
					return errors.New("fail")
				}
				return nil
			})

			if calls != tt.wantCalls {
				t.Fatalf("calls: got %d, want %d", calls, tt.wantCalls)
			}
			if result.Attempts != tt.wantAttempts {
				t.Fatalf("Attempts: got %d, want %d", result.Attempts, tt.wantAttempts)
			}
			if result.Retried != tt.wantRetried {
				t.Fatalf("Retried: got %v, want %v", result.Retried, tt.wantRetried)
			}
			if tt.wantErr == "" && result.LastError != nil {
				t.Fatalf("LastError: got %v, want nil", result.LastError)
			}
			if tt.wantErr != "" {
				if result.LastError == nil {
					t.Fatalf("LastError: got nil, want %q", tt.wantErr)
				}
				if result.LastError.Error() != tt.wantErr {
					t.Fatalf("LastError: got %q, want %q", result.LastError.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestDo_PreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Do

	calls := 0
	result := Do(ctx, Config{Attempts: 5, Delay: time.Millisecond}, func(_ context.Context, _ int) error {
		calls++
		return errors.New("fail")
	})

	if calls != 0 {
		t.Fatalf("expected 0 calls with pre-cancelled context, got %d", calls)
	}
	if result.Attempts != 0 {
		t.Fatalf("Attempts: got %d, want 0", result.Attempts)
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

	if calls >= 100 {
		t.Fatalf("expected early termination, got %d calls", calls)
	}
	if result.LastError == nil {
		t.Fatal("expected non-nil LastError")
	}
}
