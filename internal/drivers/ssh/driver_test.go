package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	valid := Config{Host: "192.0.2.10", SSHKey: "/tmp/key", KnownHosts: "/tmp/known_hosts"}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{{
		name:   "minimal config is valid",
		mutate: func(*Config) {},
	}, {
		name:    "host is required",
		mutate:  func(c *Config) { c.Host = "" },
		wantErr: "host is required",
	}, {
		name:    "ssh_key is required",
		mutate:  func(c *Config) { c.SSHKey = "" },
		wantErr: "ssh_key is required",
	}, {
		name:    "known_hosts is required",
		mutate:  func(c *Config) { c.KnownHosts = "" },
		wantErr: "known_hosts is required",
	}, {
		name:    "port out of range",
		mutate:  func(c *Config) { c.SSHPort = 70000 },
		wantErr: "ssh_port 70000 is out of range",
	}, {
		name:   "bracketed IPv6 host is accepted",
		mutate: func(c *Config) { c.Host = "[2001:db8::1]" },
	}, {
		name: "digest refs accepted for pre-pull",
		mutate: func(c *Config) {
			c.PrePullImages = []string{"cgr.dev/chainguard/wolfi-base@sha256:256c88bb066424045021090932d50be93c8e3f2ce3586707702a401b146f3e43"}
		},
	}, {
		name:    "unparseable pre-pull ref rejected",
		mutate:  func(c *Config) { c.PrePullImages = []string{"not a ref"} },
		wantErr: `pre-pull image "not a ref"`,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mutate(&cfg)
			_, err := NewDriver(cfg)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBracketedHostIsUnwrapped(t *testing.T) {
	d, err := NewDriver(Config{Host: "[2001:db8::1]", SSHKey: "/tmp/key", KnownHosts: "/tmp/known_hosts"})
	require.NoError(t, err)
	assert.Equal(t, "2001:db8::1", d.cfg.Host)
}

func TestDefaults(t *testing.T) {
	d, err := NewDriver(Config{Host: "192.0.2.10", SSHKey: "/tmp/key", KnownHosts: "/tmp/known_hosts"})
	require.NoError(t, err)
	assert.Equal(t, int32(22), d.cfg.SSHPort)
	assert.Equal(t, "ubuntu", d.cfg.SSHUser)
	assert.Equal(t, "bash", d.cfg.Shell)
	assert.NotNil(t, d.cfg.Env)
	assert.False(t, d.cfg.PullECRWithHostCredentials)
}

// TestSetupFailsClosed checks that Setup fails before any connection when
// the key or the pinned host key cannot be loaded, and that the host is
// left unset so Diagnose does not try to connect either.
func TestSetupFailsClosed(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(key, []byte(testPrivateKey), 0o600))
	otherHost := filepath.Join(dir, "known_hosts_other")
	require.NoError(t, os.WriteFile(otherHost, []byte("192.0.2.99 "+testHostKeyLine+"\n"), 0o644))

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{{
		name:    "missing key",
		cfg:     Config{Host: "192.0.2.10", SSHKey: filepath.Join(dir, "nope"), KnownHosts: otherHost},
		wantErr: "reading SSH key",
	}, {
		name:    "missing known_hosts",
		cfg:     Config{Host: "192.0.2.10", SSHKey: key, KnownHosts: filepath.Join(dir, "nope")},
		wantErr: "loading known_hosts",
	}, {
		name:    "known_hosts for another host",
		cfg:     Config{Host: "192.0.2.10", SSHKey: key, KnownHosts: otherHost},
		wantErr: "no entry for 192.0.2.10",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.OnFailure = []string{"uptime"}
			d, err := NewDriver(tc.cfg)
			require.NoError(t, err)

			err = d.Setup(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Nil(t, d.host)

			err = d.Diagnose(t.Context(), io.Discard)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "could not be loaded")

			_, err = d.Run(t.Context(), nil)
			require.Error(t, err)
		})
	}
}

// TestTeardownRunsStack checks that Teardown runs the stack, which holds the
// test containers' removal, and that IMAGETEST_SKIP_TEARDOWN leaves them in
// place.
func TestTeardownRunsStack(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(fmt.Sprintf("skip_teardown=%t", skip), func(t *testing.T) {
			d, err := NewDriver(Config{
				Host:         "192.0.2.10",
				SSHKey:       "/tmp/key",
				KnownHosts:   "/tmp/known_hosts",
				SkipTeardown: skip,
			})
			require.NoError(t, err)

			called := false
			require.NoError(t, d.stack.Add(func(context.Context) error {
				called = true
				return nil
			}))

			require.NoError(t, d.Teardown(t.Context()))
			if called == skip {
				t.Errorf("stack teardown ran = %t, want %t", called, !skip)
			}
		})
	}
}

func TestDiagnoseNothingConfigured(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, (&driver{}).Diagnose(t.Context(), &out))
	assert.Empty(t, out.String())
}

// A real ed25519 host key line, as ssh-keyscan prints it.
const testHostKeyLine = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGIx8mDY8HPYG2R+aYEKyQqLWQKCFoIk+aBF+I60wZqB"

// testPrivateKey is a throwaway ed25519 key generated for this test.
var testPrivateKey = strings.TrimSpace(`
-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACCi1kY2RDYjHyMLO/IC6FIja7ipdJemIzcHHYUobcq8XgAAAIjVelYH1XpW
BwAAAAtzc2gtZWQyNTUxOQAAACCi1kY2RDYjHyMLO/IC6FIja7ipdJemIzcHHYUobcq8Xg
AAAED1zq6jd0yfUKQAo4r1rc1wOiHT486tFhnbXnvM9ac9VKLWRjZENiMfIws78gLoUiNr
uKl0l6YjNwcdhShtyrxeAAAABHRlc3QB
-----END OPENSSH PRIVATE KEY-----
`) + "\n"

// TestSetupHostUnreachable checks that a host the driver cannot connect to
// fails Setup with HostUnreachableError, whose message starts with the
// stable marker a caller reading guest output looks for.
func TestSetupHostUnreachable(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(key, []byte(testPrivateKey), 0o600))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // test listener
	require.NoError(t, l.Close())

	known := filepath.Join(dir, "known_hosts")
	require.NoError(t, os.WriteFile(known, fmt.Appendf(nil, "[127.0.0.1]:%d %s\n", port, testHostKeyLine), 0o644))

	d, err := NewDriver(Config{Host: "127.0.0.1", SSHPort: int32(port), SSHKey: key, KnownHosts: known, LockPath: "/var/lock/imagetest/gpu"})
	require.NoError(t, err)

	err = d.Setup(t.Context())
	require.Error(t, err)

	var unreachable *HostUnreachableError
	require.ErrorAs(t, err, &unreachable)
	assert.Equal(t, "127.0.0.1", unreachable.Host)
	assert.Equal(t, int32(port), unreachable.Port)
	assert.True(t, strings.HasPrefix(err.Error(), HostUnreachablePrefix), "error %q does not start with %q", err, HostUnreachablePrefix)
	assert.Contains(t, err.Error(), "connection refused")

	// The host was loaded, so Diagnose can still try the on_failure
	// commands, and nothing was pushed on the stack: no lock, no sweep.
	assert.NotNil(t, d.host)
	require.NoError(t, d.Teardown(t.Context()))
}

// TestHostUnreachableErrorUnwraps checks that the cause stays reachable
// through errors.Is.
func TestHostUnreachableErrorUnwraps(t *testing.T) {
	cause := errors.New("dial tcp: i/o timeout")
	err := error(&HostUnreachableError{Host: "192.0.2.10", Port: 22, Err: cause})
	assert.Equal(t, "imagetest: host unreachable: 192.0.2.10 port 22: dial tcp: i/o timeout", err.Error())
	assert.ErrorIs(t, err, cause)
}

func TestLockPathDefaultsOff(t *testing.T) {
	d, err := NewDriver(Config{Host: "192.0.2.10", SSHKey: "/tmp/key", KnownHosts: "/tmp/known_hosts"})
	require.NoError(t, err)
	assert.Empty(t, d.cfg.LockPath)
}
