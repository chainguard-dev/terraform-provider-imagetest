package ec2

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeAWSTagValue(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "valid string unchanged",
			input:  "hello-world",
			maxLen: 256,
			want:   "hello-world",
		},
		{
			name:   "github bot actor",
			input:  "dependabot[bot]",
			maxLen: 256,
			want:   "dependabotbot",
		},
		{
			name:   "github actions bot",
			input:  "github-actions[bot]",
			maxLen: 256,
			want:   "github-actionsbot",
		},
		{
			name:   "valid special characters preserved",
			input:  "user_name.test:value/path=eq+plus-dash@email",
			maxLen: 256,
			want:   "user_name.test:value/path=eq+plus-dash@email",
		},
		{
			name:   "truncate to max length",
			input:  "abcdefghij",
			maxLen: 5,
			want:   "abcde",
		},
		{
			name:   "empty string",
			input:  "",
			maxLen: 256,
			want:   "",
		},
		{
			name:   "only invalid characters",
			input:  "[]{}()",
			maxLen: 256,
			want:   "",
		},
		{
			name:   "mixed valid and invalid",
			input:  "hello[world]test{foo}bar",
			maxLen: 256,
			want:   "helloworldtestfoobar",
		},
		{
			name:   "unicode letters allowed",
			input:  "héllo-wörld",
			maxLen: 256,
			want:   "héllo-wörld",
		},
		{
			name:   "whitespace preserved",
			input:  "hello world",
			maxLen: 256,
			want:   "hello world",
		},
		{
			name:   "numbers allowed",
			input:  "test123",
			maxLen: 256,
			want:   "test123",
		},
		{
			name:   "rfc3339 timestamp valid",
			input:  "2025-12-11T10:30:00Z",
			maxLen: 256,
			want:   "2025-12-11T10:30:00Z",
		},
		{
			name:   "rfc3339 with timezone offset",
			input:  "2025-12-11T10:30:00-08:00",
			maxLen: 256,
			want:   "2025-12-11T10:30:00-08:00",
		},
		{
			name:   "typical github repository",
			input:  "chainguard-dev/terraform-provider-imagetest",
			maxLen: 256,
			want:   "chainguard-dev/terraform-provider-imagetest",
		},
		{
			name:   "tag key max length",
			input:  strings.Repeat("a", 150),
			maxLen: 128,
			want:   strings.Repeat("a", 128),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAWSTagValue(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("sanitizeAWSTagValue(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestValidatePrePullImages(t *testing.T) {
	existing := &ExistingInstance{IP: "192.0.2.10", SSHKey: "/tmp/key"}
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{{
		name: "digest refs accepted in existing instance mode",
		cfg: Config{
			ExistingInstance: existing,
			PrePullImages:    []string{"cgr.dev/chainguard/wolfi-base@sha256:256c88bb066424045021090932d50be93c8e3f2ce3586707702a401b146f3e43"},
		},
	}, {
		name: "unparseable ref rejected",
		cfg: Config{
			ExistingInstance: existing,
			PrePullImages:    []string{"not a ref"},
		},
		wantErr: "pre-pull image",
	}, {
		name: "pre-pull rejected without existing instance",
		cfg: Config{
			VPCID:         "vpc-123",
			AMI:           "ami-123",
			PrePullImages: []string{"cgr.dev/chainguard/wolfi-base:latest"},
		},
		wantErr: "only supported with existing_instance",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.applyDefaults()
			err := cfg.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestTeardownExistingInstanceRunsStack checks that Teardown in existing
// instance mode runs the stack, which holds the test containers' removal,
// and that IMAGETEST_SKIP_TEARDOWN leaves them in place.
func TestTeardownExistingInstanceRunsStack(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(fmt.Sprintf("skip_teardown=%t", skip), func(t *testing.T) {
			d, err := NewDriver(Config{
				ExistingInstance: &ExistingInstance{IP: "192.0.2.10", SSHKey: "/tmp/key"},
				SkipTeardown:     skip,
			}, nil, nil)
			if err != nil {
				t.Fatalf("NewDriver: %v", err)
			}
			d.host = d.newHost(d.cfg.ExistingInstance.IP, d.cfg.ExistingInstance.SSHKey, nil)

			called := false
			if err := d.stack.Add(func(context.Context) error {
				called = true
				return nil
			}); err != nil {
				t.Fatalf("stack.Add: %v", err)
			}

			if err := d.Teardown(t.Context()); err != nil {
				t.Fatalf("Teardown: %v", err)
			}
			if called == skip {
				t.Errorf("stack teardown ran = %t, want %t", called, !skip)
			}
		})
	}
}

// TestDiagnose checks that on_failure diagnostics report a missing instance
// instead of connecting, whichever way Setup failed before the host existed.
func TestDiagnose(t *testing.T) {
	t.Run("nothing configured, nothing to do", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&driver{}).Diagnose(t.Context(), &out))
		assert.Empty(t, out.String())
	})

	cmds := []string{"uptime"}
	for name, d := range map[string]*driver{
		"new instance never created":   {cfg: Config{OnFailure: cmds}},
		"new instance never reachable": {cfg: Config{OnFailure: cmds}, instance: &instance{}},
		// Setup fails reading the key before the host is set.
		"existing instance without key": {cfg: Config{OnFailure: cmds, ExistingInstance: &ExistingInstance{IP: "127.0.0.1", SSHKey: "/nope"}}},
	} {
		t.Run(name, func(t *testing.T) {
			err := d.Diagnose(t.Context(), io.Discard)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not created")
		})
	}
}
