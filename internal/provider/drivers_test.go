package provider

import (
	"os"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestLoadDriverK3sInDockerKubeconfigCleanup checks that the temp file
// LoadDriver reserves for the k3s_in_docker kubeconfig does not outlive the
// driver: it is removed by Teardown, and not left behind when LoadDriver
// itself fails. Neither path needs docker: Teardown of a driver that was
// never set up has nothing on its stack.
func TestLoadDriverK3sInDockerKubeconfigCleanup(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *K3sInDockerDriverResourceModel
		wantLoad bool // LoadDriver returns a driver
	}{{
		name:     "teardown removes the kubeconfig file",
		cfg:      &K3sInDockerDriverResourceModel{},
		wantLoad: true,
	}, {
		name: "invalid setup timeout leaves no kubeconfig file",
		cfg: &K3sInDockerDriverResourceModel{
			Timeouts: &DriverTimeoutsResourceModel{Setup: types.StringValue("not-a-duration")},
		},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx := t.Context()

			repo, err := name.NewRepository("registry.example.com/imagetest")
			if err != nil {
				t.Fatalf("parsing repository: %v", err)
			}
			tr := TestsResource{repo: repo}
			dr, err := tr.LoadDriver(ctx, &TestsResourceModel{
				Id:      types.StringValue("k3s-cleanup-test"),
				Driver:  DriverK3sInDocker,
				Drivers: &TestsDriversResourceModel{K3sInDocker: tc.cfg},
			})
			if (err == nil) != tc.wantLoad {
				t.Fatalf("LoadDriver(cfg=%+v): got err=%v, want success=%t", tc.cfg, err, tc.wantLoad)
			}
			if dr != nil {
				if err := dr.Teardown(ctx); err != nil {
					t.Errorf("Teardown: %v", err)
				}
			}

			entries, rerr := os.ReadDir(tmp)
			if rerr != nil {
				t.Fatalf("reading TMPDIR %s: %v", tmp, rerr)
			}
			var left []string
			for _, e := range entries {
				left = append(left, e.Name())
			}
			if len(left) > 0 {
				t.Errorf("cfg=%+v: TMPDIR has leftover entries %v, want none (LoadDriver err=%v)", tc.cfg, left, err)
			}
		})
	}
}
