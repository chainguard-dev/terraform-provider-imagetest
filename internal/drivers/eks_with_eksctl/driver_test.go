package ekswitheksctl

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
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

func TestEksctlArgs(t *testing.T) {
	tests := []struct {
		name     string
		timeouts drivers.Timeouts
		in       []string
		want     []string
	}{
		{
			name: "create gets debug flags and no timeout when setup is unset",
			in:   []string{"create", "cluster", "--config-file=x"},
			want: []string{"create", "cluster", "--config-file=x", "--color", "false", "--dumpLogs", "--verbose", "4"},
		},
		{
			name:     "create gets the setup timeout",
			timeouts: drivers.Timeouts{Setup: 30 * time.Minute, Teardown: 5 * time.Minute},
			in:       []string{"create", "nodegroup"},
			want:     []string{"create", "nodegroup", "--color", "false", "--dumpLogs", "--verbose", "4", "--timeout", "30m0s"},
		},
		{
			name: "delete is always bounded by the default teardown timeout",
			in:   []string{"delete", "cluster", "--name", "c"},
			want: []string{"delete", "cluster", "--name", "c", "--color", "false", "--timeout", teardownTimeoutDefault.String()},
		},
		{
			name:     "delete uses the teardown timeout, not the setup timeout",
			timeouts: drivers.Timeouts{Setup: 30 * time.Minute, Teardown: 5 * time.Minute},
			in:       []string{"delete", "nodegroup"},
			want:     []string{"delete", "nodegroup", "--color", "false", "--timeout", "5m0s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &driver{timeouts: tt.timeouts}
			if got := k.eksctlArgs(context.Background(), tt.in...); !slices.Equal(got, tt.want) {
				t.Errorf("eksctlArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEksctlArgsDeleteUsesRemainingDeadline(t *testing.T) {
	k := &driver{timeouts: drivers.Timeouts{Teardown: 20 * time.Minute}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	got := k.eksctlArgs(ctx, "delete", "cluster")
	timeout, err := time.ParseDuration(got[len(got)-1])
	if err != nil || got[len(got)-2] != "--timeout" {
		t.Fatalf("eksctlArgs() = %q, want trailing --timeout <duration>", got)
	}
	// Remaining time minus the grace, not the configured 20m.
	want := 10*time.Minute - eksctlTimeoutGrace
	if timeout > want || timeout < want-5*time.Second {
		t.Errorf("--timeout = %s, want about %s", timeout, want)
	}

	// An (almost) expired deadline still yields a valid, positive duration.
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	got = k.eksctlArgs(ctx, "delete", "cluster")
	if got[len(got)-1] != "1s" {
		t.Errorf("--timeout = %s past the deadline, want 1s", got[len(got)-1])
	}
}

func TestTeardownCommands(t *testing.T) {
	tests := []struct {
		name string
		k    *driver
		want [][]string
	}{
		{
			name: "nodegroup is deleted without draining before the cluster",
			k:    &driver{clusterName: "imagetest-abc", region: "eu-west-1", nodeGroup: "ng-123"},
			want: [][]string{
				{"delete", "nodegroup", "--cluster", "imagetest-abc", "--region", "eu-west-1", "--name", "ng-123", "--drain=false", "--wait"},
				{"delete", "cluster", "--name", "imagetest-abc", "--region", "eu-west-1", "--force", "--disable-nodegroup-eviction", "--parallel", "25", "--wait"},
			},
		},
		{
			name: "no nodegroup delete when none was created",
			k:    &driver{clusterName: "imagetest-abc", region: "us-west-2"},
			want: [][]string{
				{"delete", "cluster", "--name", "imagetest-abc", "--region", "us-west-2", "--force", "--disable-nodegroup-eviction", "--parallel", "25", "--wait"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.k.teardownCommands()
			if !slices.EqualFunc(got, tt.want, slices.Equal) {
				t.Errorf("teardownCommands() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTeardown checks that only the cluster delete decides the outcome: a
// failed nodegroup delete (e.g. Setup failed before the stack existed) is not
// a teardown failure when the cluster delete succeeds, and every command runs
// regardless of earlier failures.
func TestTeardown(t *testing.T) {
	errNG := errors.New("nodegroup not found")
	errCluster := errors.New("stack DELETE_FAILED")

	tests := []struct {
		name     string
		fail     map[string]error // keyed by args[1]
		wantRuns []string
		wantErr  error // nil, or an error that must be in the chain
	}{
		{
			name:     "both succeed",
			wantRuns: []string{"nodegroup", "cluster"},
		},
		{
			name:     "nodegroup delete failure is not a teardown failure when the cluster delete succeeds",
			fail:     map[string]error{"nodegroup": errNG},
			wantRuns: []string{"nodegroup", "cluster"},
		},
		{
			name:     "cluster delete failure is a teardown failure",
			fail:     map[string]error{"cluster": errCluster},
			wantRuns: []string{"nodegroup", "cluster"},
			wantErr:  errCluster,
		},
		{
			name:     "both failures are reported when the cluster delete fails",
			fail:     map[string]error{"nodegroup": errNG, "cluster": errCluster},
			wantRuns: []string{"nodegroup", "cluster"},
			wantErr:  errNG,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var runs []string
			k := &driver{clusterName: "imagetest-abc", region: "us-west-2", nodeGroup: "ng-1"}
			k.run = func(_ context.Context, args ...string) error {
				runs = append(runs, args[1])
				return tt.fail[args[1]]
			}

			err := k.Teardown(context.Background())
			if !slices.Equal(runs, tt.wantRuns) {
				t.Errorf("ran %q, want %q", runs, tt.wantRuns)
			}
			switch {
			case tt.wantErr == nil && err != nil:
				t.Errorf("Teardown() = %v, want nil", err)
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("Teardown() = %v, want error wrapping %v", err, tt.wantErr)
			}
		})
	}
}

func TestManualCleanup(t *testing.T) {
	k := &driver{clusterName: "imagetest-abc", region: "us-west-2", nodeGroup: "ng-1"}
	got := k.manualCleanup()
	want := "eksctl delete nodegroup --cluster imagetest-abc --region us-west-2 --name ng-1 --drain=false --wait && eksctl delete cluster --name imagetest-abc --region us-west-2 --force --disable-nodegroup-eviction --parallel 25 --wait"
	if got != want {
		t.Errorf("manualCleanup() =\n%s\nwant\n%s", got, want)
	}
}
