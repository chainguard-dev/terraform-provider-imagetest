package ekswitheksctl

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTail(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		max  int
		want string
	}{
		{
			name: "short output is unchanged",
			in:   []byte("all good"),
			max:  64,
			want: "all good",
		},
		{
			name: "exactly max is unchanged",
			in:   bytes.Repeat([]byte("x"), 8),
			max:  8,
			want: "xxxxxxxx",
		},
		{
			name: "long output keeps the tail and notes truncation",
			in:   []byte(strings.Repeat("noise\n", 100) + "the actual error"),
			max:  16,
			want: "[... 600 bytes truncated ...]\nthe actual error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(tail(tt.in, tt.max)); got != tt.want {
				t.Errorf("tail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShouldRetryLoggingUpdate(t *testing.T) {
	// The full conflict line as eksctl surfaces it via the wrapped
	// CombinedOutput error.
	conflict := fmt.Errorf("eksctl update-cluster-logging: %w", errors.New(
		`eksctl [utils update-cluster-logging]: exit status 1: 2026-09-04 12:00:00 [!]  retryable error (ResourceInUseException: Cannot LoggingUpdate because cluster imagetest-1234 currently has an update in progress) from eks/UpdateClusterConfig`))

	tests := []struct {
		name    string
		err     error
		elapsed time.Duration
		want    bool
	}{
		{
			name:    "nil error is not retried",
			err:     nil,
			elapsed: 0,
			want:    false,
		},
		{
			name:    "update in progress conflict is retried",
			err:     conflict,
			elapsed: time.Minute,
			want:    true,
		},
		{
			name: "truncated output keeping only the message tail is retried",
			err: fmt.Errorf("eksctl update-cluster-logging: %w", errors.New(
				"eksctl [utils update-cluster-logging]: exit status 1: [... 31337 bytes truncated ...]\ncluster imagetest-1234 currently has an update in progress) from eks/UpdateClusterConfig")),
			elapsed: time.Minute,
			want:    true,
		},
		{
			name: "bare exception name without the message is retried",
			err: fmt.Errorf("eksctl update-cluster-logging: %w",
				errors.New("eksctl [utils update-cluster-logging]: exit status 1: ResourceInUseException")),
			elapsed: time.Minute,
			want:    true,
		},
		{
			name: "unrelated error is not retried",
			err: fmt.Errorf("eksctl update-cluster-logging: %w",
				errors.New("eksctl [utils update-cluster-logging]: exit status 1: AccessDeniedException: not authorized to perform eks:UpdateClusterConfig")),
			elapsed: time.Minute,
			want:    false,
		},
		{
			name:    "conflict past the retry ceiling is not retried",
			err:     conflict,
			elapsed: loggingUpdateRetryCeiling,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRetryLoggingUpdate(tt.err, tt.elapsed); got != tt.want {
				t.Errorf("shouldRetryLoggingUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}
