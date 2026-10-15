package ec2

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
)

func TestTailLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{
			name: "empty input returns placeholder",
			in:   "",
			n:    10,
			want: "(empty)",
		},
		{
			name: "fewer lines than tail returns full input (trailing newline stripped)",
			in:   "one\ntwo\nthree\n",
			n:    10,
			want: "one\ntwo\nthree",
		},
		{
			name: "exactly n lines returns full input",
			in:   "one\ntwo\nthree",
			n:    3,
			want: "one\ntwo\nthree",
		},
		{
			name: "more lines than n returns last n with truncation marker",
			in:   "a\nb\nc\nd\ne",
			n:    2,
			want: fmt.Sprintf("(...truncated to last %d of %d lines...)\n%s", 2, 5, "d\ne"),
		},
		{
			name: "single line input shorter than n",
			in:   "only one line",
			n:    5,
			want: "only one line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tailLines(tt.in, tt.n)
			if got != tt.want {
				t.Errorf("tailLines() = %q, want %q", got, tt.want)
			}
		})
	}
}

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

func TestEnvExports(t *testing.T) {
	if got := envExports(nil); len(got) != 0 {
		t.Errorf("envExports(nil) = %q, want none", got)
	}
	got := envExports(map[string]string{"B": "two words", "A": "1", "C": `it's $HOME`})
	want := []string{"export A=1", "export B='two words'", `export C='it'\''s $HOME'`}
	if !slices.Equal(got, want) {
		t.Errorf("envExports = %q, want %q", got, want)
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
			d.existingIP = d.cfg.ExistingInstance.IP

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

// TestCollectGPUInfoExistingInstanceNonFatal checks that GPU info collection
// in existing instance mode returns normally when the SSH connection fails,
// so a host without reachable SSH, or without nvidia-smi, cannot fail Setup.
func TestCollectGPUInfoExistingInstanceNonFatal(t *testing.T) {
	// Reserve a port and release it, so the connection is refused.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr: %T, want *net.TCPAddr", l.Addr())
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	d, err := NewDriver(Config{
		ExistingInstance: &ExistingInstance{IP: "127.0.0.1", SSHKey: "/tmp/key"},
		SSHPort:          int32(addr.Port),
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	d.existingIP = d.cfg.ExistingInstance.IP

	// Must not panic or hang; failures are logged, never returned.
	d.collectGPUInfo(t.Context())
}
