package ekswitheksctl

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
// regardless of earlier failures. The kubeconfig Setup created is removed
// once the deletes have run, whatever their outcome, and kept along with the
// cluster when IMAGETEST_EKS_SKIP_TEARDOWN is set.
func TestTeardown(t *testing.T) {
	errNG := errors.New("nodegroup not found")
	errCluster := errors.New("stack DELETE_FAILED")

	tests := []struct {
		name     string
		fail     map[string]error // keyed by args[1]
		wantRuns []string
		wantErr  error  // nil, or an error that must be in the chain
		skipEnv  string // IMAGETEST_EKS_SKIP_TEARDOWN
		wantKept bool   // the kubeconfig survives Teardown
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
		{
			name:     "skipped teardown keeps the cluster and its kubeconfig",
			skipEnv:  "true",
			wantKept: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("IMAGETEST_EKS_SKIP_TEARDOWN", tt.skipEnv)
			kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
			if err := os.WriteFile(kubeconfig, []byte("apiVersion: v1\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			var runs []string
			k := &driver{clusterName: "imagetest-abc", region: "us-west-2", nodeGroup: "ng-1", kubeconfig: kubeconfig}
			k.run = func(_ context.Context, args ...string) error {
				// eksctl gets the kubeconfig (KUBECONFIG) for every delete.
				if _, err := os.Stat(kubeconfig); err != nil {
					t.Errorf("eksctl %s run without its kubeconfig: %v", args[1], err)
				}
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
			_, serr := os.Stat(kubeconfig)
			if kept := serr == nil; kept != tt.wantKept {
				t.Errorf("fail=%v skip=%q: kubeconfig kept=%t (stat err=%v), want kept=%t", tt.fail, tt.skipEnv, kept, serr, tt.wantKept)
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

// TestCreateKubeconfig checks the kubeconfig file Setup hands to eksctl is a
// fresh private temp file per driver, not a predictable shared path.
func TestCreateKubeconfig(t *testing.T) {
	tests := []struct {
		name    string
		cluster string
	}{
		{name: "random cluster name", cluster: "imagetest-0123"},
		{name: "fixed IMAGETEST_EKS_CLUSTER name", cluster: "my-long-lived-cluster"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)

			a, b := &driver{clusterName: tt.cluster}, &driver{clusterName: tt.cluster}
			for _, k := range []*driver{a, b} {
				if err := k.createKubeconfig(); err != nil {
					t.Fatalf("createKubeconfig(cluster=%q): %v", tt.cluster, err)
				}
				fi, err := os.Stat(k.kubeconfig)
				if err != nil {
					t.Fatalf("stat kubeconfig %q: %v", k.kubeconfig, err)
				}
				if got := fi.Mode().Perm(); got != 0o600 {
					t.Errorf("kubeconfig %q mode = %#o, want %#o", k.kubeconfig, got, 0o600)
				}
				if filepath.Dir(k.kubeconfig) != tmp {
					t.Errorf("kubeconfig = %q, want it in TMPDIR %q", k.kubeconfig, tmp)
				}
			}
			if a.kubeconfig == b.kubeconfig {
				t.Errorf("two drivers for cluster %q share kubeconfig %q, want distinct files", tt.cluster, a.kubeconfig)
			}

			for _, k := range []*driver{a, b} {
				if err := k.removeKubeconfig(); err != nil {
					t.Errorf("removeKubeconfig: %v", err)
				}
			}
			entries, err := os.ReadDir(tmp)
			if err != nil {
				t.Fatalf("reading TMPDIR: %v", err)
			}
			if len(entries) > 0 {
				t.Errorf("TMPDIR has %d leftover entries after removeKubeconfig, want none", len(entries))
			}
		})
	}
}
