package sshhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sshhost/sshtest"
	"golang.org/x/crypto/ssh"
)

func TestLockCommand(t *testing.T) {
	got := lockCommand("/var/lock/imagetest/gpu")
	want := "flock -x /var/lock/imagetest/gpu -c 'echo imagetest-lock-held; exec cat'"
	if got != want {
		t.Fatalf("lockCommand() = %q, want %q", got, want)
	}
	// A path with a space is quoted for the shell.
	if got := lockCommand("/tmp/a b"); !strings.HasPrefix(got, "flock -x '/tmp/a b' -c") {
		t.Fatalf("lockCommand() = %q, path not quoted", got)
	}
}

// lockHost returns a Host that connects to srv and locks the test path.
func lockHost(srv *sshtest.Server) *Host {
	return &Host{
		Name:     "imagetest-ssh-test",
		Addr:     srv.Addr,
		Port:     srv.Port,
		User:     "test",
		Signer:   srv.Signer,
		HostKeys: []ssh.PublicKey{srv.HostKey},
		LockPath: "/var/lock/imagetest/gpu",
	}
}

// holdUntilEOF is a lock holder that gets the lock at once: it prints the
// token, then holds until stdin closes, like cat.
func holdUntilEOF(_ string, ch ssh.Channel) uint32 {
	_, _ = fmt.Fprintln(ch, LockToken)
	_, _ = io.Copy(io.Discard, ch)
	return 0
}

// TestAcquireLockAndRelease checks that the lock session runs the flock
// command, reports the lock held once the token arrives, and that Release
// ends the holder by closing its stdin.
func TestAcquireLockAndRelease(t *testing.T) {
	released := make(chan struct{})
	srv := sshtest.New(t, func(cmd string, ch ssh.Channel) uint32 {
		status := holdUntilEOF(cmd, ch)
		close(released)
		return status
	})
	h := lockHost(srv)

	lock, err := h.AcquireLock(t.Context())
	if err != nil {
		t.Fatalf("AcquireLock() = %v", err)
	}
	if lock.Path != h.LockPath {
		t.Errorf("lock.Path = %q, want %q", lock.Path, h.LockPath)
	}
	if cmds := srv.Commands(); len(cmds) != 1 || cmds[0] != lockCommand(h.LockPath) {
		t.Errorf("server received %q, want %q", cmds, lockCommand(h.LockPath))
	}

	if err := lock.Release(t.Context()); err != nil {
		t.Fatalf("Release() = %v", err)
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("holder did not see stdin close after Release")
	}
}

// TestAcquireLockWaits checks that a busy lock is waited for, that the time
// waited is measured, and that the wait ends with ctx.
func TestAcquireLockWaits(t *testing.T) {
	const busy = 300 * time.Millisecond
	srv := sshtest.New(t, func(cmd string, ch ssh.Channel) uint32 {
		time.Sleep(busy) // another holder has the lock
		return holdUntilEOF(cmd, ch)
	})
	h := lockHost(srv)

	lock, err := h.AcquireLock(t.Context())
	if err != nil {
		t.Fatalf("AcquireLock() = %v", err)
	}
	defer lock.Release(t.Context()) //nolint:errcheck // test cleanup
	if lock.Waited < busy {
		t.Errorf("lock.Waited = %s, want at least %s", lock.Waited, busy)
	}

	// Another run that cannot wait that long gives up with the context's
	// error, and its holder is ended with the connection.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.AcquireLock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireLock() under a short deadline = %v, want context.DeadlineExceeded", err)
	}
}

// TestAcquireLockHolderFails checks that a holder which exits without
// taking the lock, such as when flock is not installed, is reported with
// its stderr and exit status instead of being waited for.
func TestAcquireLockHolderFails(t *testing.T) {
	srv := sshtest.New(t, func(_ string, ch ssh.Channel) uint32 {
		_, _ = fmt.Fprintln(ch.Stderr(), "sh: 1: flock: not found")
		return 127
	})
	h := lockHost(srv)

	_, err := h.AcquireLock(t.Context())
	if err == nil {
		t.Fatal("AcquireLock() = nil, want error")
	}
	for _, want := range []string{"taking lock /var/lock/imagetest/gpu", "flock: not found", "127"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("AcquireLock() = %v, want it to mention %q", err, want)
		}
	}
}

// TestAcquireLockUnreachable checks that a host that cannot be reached
// fails the lock without hanging.
func TestAcquireLockUnreachable(t *testing.T) {
	h := &Host{Addr: "127.0.0.1", Port: closedPort(t), User: "nobody", LockPath: "/tmp/lock"}
	if _, err := h.AcquireLock(t.Context()); err == nil || !strings.Contains(err.Error(), "SSH connection failed") {
		t.Fatalf("AcquireLock() = %v, want connection error", err)
	}
}
