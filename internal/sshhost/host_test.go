package sshhost

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
)

// closedPort returns a TCP port on loopback that nothing listens on.
func closedPort(t *testing.T) int32 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	if !ok {
		t.Fatalf("unexpected listener address %T", l.Addr())
	}
	return int32(addr.Port)
}

// TestCollectGPUInfoNonFatal checks that GPU info collection against an
// unreachable host returns without error: it is diagnostic output, not a
// precondition of the run.
func TestCollectGPUInfoNonFatal(t *testing.T) {
	h := &Host{Driver: "test", Addr: "127.0.0.1", Port: closedPort(t), User: "nobody"}
	h.CollectGPUInfo(t.Context())
}

// TestRunSetupCommandsNoneIsNoop checks that an empty command list opens no
// connection: the host is unreachable and the call must still succeed.
func TestRunSetupCommandsNoneIsNoop(t *testing.T) {
	h := &Host{Addr: "127.0.0.1", Port: closedPort(t), User: "nobody"}
	if err := h.RunSetupCommands(t.Context()); err != nil {
		t.Fatalf("RunSetupCommands() = %v, want nil", err)
	}
}

// TestPrePullImagesParsesBeforeConnecting checks that a bad reference is
// rejected before any Docker connection, and that an empty list is a no-op.
func TestPrePullImagesParsesBeforeConnecting(t *testing.T) {
	h := &Host{Addr: "127.0.0.1", Port: closedPort(t), User: "nobody", KeyPath: "/nope"}
	if err := h.PrePullImages(t.Context(), nil); err != nil {
		t.Fatalf("PrePullImages(nil) = %v, want nil", err)
	}
	err := h.PrePullImages(t.Context(), []string{"cgr.dev/chainguard/wolfi-base:latest", "not a ref"})
	if err == nil || !strings.Contains(err.Error(), `parsing pre-pull image "not a ref"`) {
		t.Fatalf("PrePullImages() = %v, want parse error", err)
	}
}

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
