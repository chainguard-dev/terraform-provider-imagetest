package k3sindocker

import (
	"slices"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
)

// TestLocalFiles checks the host kubeconfig is reported as a file Teardown
// removes, so the provider can name it at the end of a run that kept it.
func TestLocalFiles(t *testing.T) {
	tests := []struct {
		name       string
		kubeconfig string // kubeconfigWritePath
		want       []drivers.LocalFile
	}{{
		name: "nothing is reported when no host kubeconfig is written",
	}, {
		name:       "the host kubeconfig is reported with its cluster",
		kubeconfig: "/tmp/imagetest-k3s-in-docker123",
		want: []drivers.LocalFile{{
			Path:        "/tmp/imagetest-k3s-in-docker123",
			Description: "kubeconfig for k3s_in_docker cluster imagetest-k3s-abc",
		}},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k := &driver{name: "imagetest-k3s-abc", kubeconfigWritePath: tc.kubeconfig}
			if got := k.LocalFiles(); !slices.Equal(got, tc.want) {
				t.Errorf("LocalFiles() with kubeconfigWritePath=%q: got %+v, want %+v", tc.kubeconfig, got, tc.want)
			}
		})
	}
}
