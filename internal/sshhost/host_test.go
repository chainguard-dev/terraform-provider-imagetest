package sshhost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// TestLoad checks that Load fails closed on a missing key or a known_hosts
// file without the host, and leaves HostKeys empty when no file is given.
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte(testPrivateKey), 0o600); err != nil {
		t.Fatal(err)
	}
	known := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(known, []byte("192.0.2.10 "+testHostKeyLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		h        Host
		wantErr  string
		wantKeys int
	}{{
		name: "key only, unchecked host key",
		h:    Host{Addr: "192.0.2.10", Port: 22, KeyPath: key},
	}, {
		name:     "key and pinned host",
		h:        Host{Addr: "192.0.2.10", Port: 22, KeyPath: key, KnownHosts: known},
		wantKeys: 1,
	}, {
		name:    "missing key",
		h:       Host{Addr: "192.0.2.10", Port: 22, KeyPath: filepath.Join(dir, "nope"), KnownHosts: known},
		wantErr: "reading SSH key",
	}, {
		name:    "known_hosts for another host",
		h:       Host{Addr: "192.0.2.99", Port: 22, KeyPath: key, KnownHosts: known},
		wantErr: "no entry for 192.0.2.99",
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h.Load()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load() = %v, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			if tc.h.Signer == nil {
				t.Error("Signer not set")
			}
			if len(tc.h.HostKeys) != tc.wantKeys {
				t.Errorf("HostKeys = %d, want %d", len(tc.h.HostKeys), tc.wantKeys)
			}
		})
	}
}

// TestConnectHonorsContext checks that a connection attempt under a finished
// context fails with that context's error.
func TestConnectHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h := &Host{Addr: "127.0.0.1", Port: closedPort(t), User: "nobody"}
	if _, _, err := h.connect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("connect() = %v, want context.Canceled", err)
	}
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
