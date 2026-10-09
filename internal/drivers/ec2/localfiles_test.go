package ec2

import (
	"slices"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
)

// TestLocalFiles checks the driver reports the SSH private key it generated,
// so the provider can name it at the end of a run that kept it, and never a
// key the user supplied.
func TestLocalFiles(t *testing.T) {
	tests := []struct {
		name string
		d    *driver
		want []drivers.LocalFile
	}{{
		name: "nothing is reported before Setup creates a key pair",
		d:    &driver{name: "imagetest-ec2-abc"},
	}, {
		name: "existing-instance mode never reports the user's own SSH key",
		d: &driver{
			name: "imagetest-ec2-abc",
			cfg:  Config{ExistingInstance: &ExistingInstance{IP: "192.0.2.1", SSHKey: "/home/user/.ssh/id_ed25519"}},
		},
	}, {
		name: "a key pair whose file was never written reports nothing",
		d:    &driver{name: "imagetest-ec2-abc", key: &keyPair{name: "imagetest-ec2-abc-key"}},
	}, {
		name: "the generated private key is reported with its key pair",
		d:    &driver{name: "imagetest-ec2-abc", key: &keyPair{name: "imagetest-ec2-abc-key", path: "/tmp/imagetest-ec2-abc-key-123.pem"}},
		want: []drivers.LocalFile{{
			Path:        "/tmp/imagetest-ec2-abc-key-123.pem",
			Description: "SSH private key for EC2 key pair imagetest-ec2-abc-key",
		}},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.LocalFiles(); !slices.Equal(got, tc.want) {
				t.Errorf("LocalFiles() for driver %q (key=%+v, existing=%+v): got %+v, want %+v",
					tc.d.name, tc.d.key, tc.d.cfg.ExistingInstance, got, tc.want)
			}
		})
	}
}
