package aks

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
)

// TestKubeconfigLifetime checks the kubeconfig Setup writes the cluster's
// admin credentials to: it is a private temp file, removed by the teardown
// stack unless the cluster itself is kept (IMAGETEST_AKS_SKIP_TEARDOWN).
func TestKubeconfigLifetime(t *testing.T) {
	tests := []struct {
		name     string
		skipEnv  string // IMAGETEST_AKS_SKIP_TEARDOWN
		wantKept bool
	}{{
		name: "teardown removes the kubeconfig",
	}, {
		name:     "kubeconfig is kept with the cluster when AKS teardown is skipped",
		skipEnv:  "true",
		wantKept: true,
	}, {
		name:    "only the exact value true skips teardown",
		skipEnv: "1",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			t.Setenv("IMAGETEST_AKS_SKIP_TEARDOWN", tc.skipEnv)

			k := &driver{clusterName: "imagetest-test", stack: harness.NewStack()}
			if err := k.createKubeconfig(); err != nil {
				t.Fatalf("createKubeconfig: %v", err)
			}
			if filepath.Dir(k.kubeconfig) != tmp {
				t.Errorf("kubeconfig = %q, want it in TMPDIR %q", k.kubeconfig, tmp)
			}
			fi, err := os.Stat(k.kubeconfig)
			if err != nil {
				t.Fatalf("stat kubeconfig %q: %v", k.kubeconfig, err)
			}
			if got := fi.Mode().Perm(); got != 0o600 {
				t.Errorf("kubeconfig mode = %#o, want %#o", got, 0o600)
			}

			if err := k.stack.Teardown(t.Context()); err != nil {
				t.Errorf("stack teardown: %v", err)
			}

			_, err = os.Stat(k.kubeconfig)
			if kept := err == nil; kept != tc.wantKept {
				t.Errorf("IMAGETEST_AKS_SKIP_TEARDOWN=%q: kubeconfig kept=%t (stat err=%v), want kept=%t", tc.skipEnv, kept, err, tc.wantKept)
			}
		})
	}
}

// TestLocalFiles checks the kubeconfig is reported as a host file teardown
// removes, so the provider can name it at the end of a run that kept it.
func TestLocalFiles(t *testing.T) {
	tests := []struct {
		name       string
		kubeconfig string
		want       []drivers.LocalFile
	}{{
		name: "nothing is reported before Setup creates the kubeconfig",
	}, {
		name:       "the kubeconfig is reported with its cluster",
		kubeconfig: "/tmp/imagetest-test-kubeconfig-123",
		want: []drivers.LocalFile{{
			Path:        "/tmp/imagetest-test-kubeconfig-123",
			Description: "admin kubeconfig for AKS cluster imagetest-test",
		}},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k := &driver{clusterName: "imagetest-test", kubeconfig: tc.kubeconfig}
			if got := k.LocalFiles(); !slices.Equal(got, tc.want) {
				t.Errorf("LocalFiles() with kubeconfig=%q: got %+v, want %+v", tc.kubeconfig, got, tc.want)
			}
		})
	}
}
