package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sshhost"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sshhost/sshtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// busyHolder plays a lock another run holds: it never prints the token
// and holds the session until the client gives up.
func busyHolder(_ string, ch ssh.Channel) uint32 {
	_, _ = io.Copy(io.Discard, ch)
	return 0
}

// freeHolder plays a free lock: it prints the token at once, then holds
// until stdin closes, like cat.
func freeHolder(_ string, ch ssh.Channel) uint32 {
	_, _ = fmt.Fprintln(ch, sshhost.LockToken)
	_, _ = io.Copy(io.Discard, ch)
	return 0
}

func newLockedDriver(t *testing.T, srv *sshtest.Server, lockTimeout time.Duration) *driver {
	t.Helper()
	key, known := srv.ClientFiles(t.TempDir())
	d, err := NewDriver(Config{
		Host:        srv.Addr,
		SSHPort:     srv.Port,
		SSHUser:     "test",
		SSHKey:      key,
		KnownHosts:  known,
		LockPath:    "/var/lock/imagetest/gpu",
		LockTimeout: lockTimeout,
	})
	require.NoError(t, err)
	return d
}

func TestLockTimeoutDefault(t *testing.T) {
	d, err := NewDriver(Config{Host: "192.0.2.10", SSHKey: "/tmp/key", KnownHosts: "/tmp/known_hosts"})
	require.NoError(t, err)
	assert.Equal(t, DefaultLockTimeout, d.cfg.LockTimeout)

	_, err = NewDriver(Config{Host: "192.0.2.10", SSHKey: "/tmp/key", KnownHosts: "/tmp/known_hosts", LockTimeout: -time.Second})
	require.ErrorContains(t, err, "lock_timeout -1s is negative")
}

// TestAdmitHoldsLock checks that Admit takes the lock over one session and
// that Teardown releases it by ending the holder.
func TestAdmitHoldsLock(t *testing.T) {
	released := make(chan struct{})
	srv := sshtest.New(t, func(cmd string, ch ssh.Channel) uint32 {
		defer close(released)
		return freeHolder(cmd, ch)
	})
	d := newLockedDriver(t, srv, time.Minute)

	require.NoError(t, d.Admit(t.Context()))
	assert.True(t, d.admitted)
	require.Len(t, srv.Commands(), 1)
	assert.Contains(t, srv.Commands()[0], "flock -x /var/lock/imagetest/gpu")

	require.NoError(t, d.Teardown(t.Context()))
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("holder did not see stdin close after Teardown")
	}
}

// TestAdmitLockTimeout checks that a lock another run holds fails Admit,
// and a Setup that admits itself, with LockTimeoutError once lock_timeout
// has passed, under a caller context that is still live: the bound is the
// driver's own and not the test timeout.
func TestAdmitLockTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*driver, context.Context) error
	}{
		{name: "Admit", run: (*driver).Admit},
		{name: "Setup", run: (*driver).Setup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := sshtest.New(t, busyHolder)
			d := newLockedDriver(t, srv, 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := time.Now()
			err := tc.run(d, ctx)
			require.Error(t, err)

			var lockErr *LockTimeoutError
			require.ErrorAs(t, err, &lockErr)
			assert.Equal(t, "/var/lock/imagetest/gpu", lockErr.Path)
			assert.Equal(t, 100*time.Millisecond, lockErr.Timeout)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.True(t, strings.HasPrefix(err.Error(), LockTimeoutPrefix), "error %q does not start with %q", err, LockTimeoutPrefix)
			assert.Less(t, time.Since(start), 5*time.Second, "gave up on lock_timeout, not on the caller's deadline")
			assert.NoError(t, ctx.Err(), "the caller's context is untouched")

			// Nothing was pushed on the stack, so teardown has nothing to
			// release.
			assert.False(t, d.admitted)
			require.NoError(t, d.Teardown(t.Context()))
		})
	}
}

// TestAdmitCancelledIsNotTimeout checks that a caller giving up mid-wait
// is reported as its cancel and not as the lock timing out.
func TestAdmitCancelledIsNotTimeout(t *testing.T) {
	srv := sshtest.New(t, busyHolder)
	d := newLockedDriver(t, srv, time.Minute)

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	err := d.Admit(ctx)
	require.Error(t, err)

	var lockErr *LockTimeoutError
	assert.False(t, errors.As(err, &lockErr), "cancel reported as %v", err)
	assert.ErrorIs(t, err, context.Canceled)
}
