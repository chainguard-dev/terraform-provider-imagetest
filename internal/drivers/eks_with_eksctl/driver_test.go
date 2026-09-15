package ekswitheksctl

import (
	"bytes"
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
			if got := k.eksctlArgs(tt.in...); !slices.Equal(got, tt.want) {
				t.Errorf("eksctlArgs() = %q, want %q", got, tt.want)
			}
		})
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
