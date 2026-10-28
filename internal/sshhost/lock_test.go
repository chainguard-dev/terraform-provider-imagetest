package sshhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	issh "github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
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

// execHandler plays the lock holder for one exec request. It returns the
// exit status; the channel is closed by the server afterwards.
type execHandler func(cmd string, ch ssh.Channel) uint32

// lockServer is an SSH server that accepts any key and answers exec
// requests with handle. Host returns a Host that connects to it.
type lockServer struct {
	t       *testing.T
	l       net.Listener
	hostKey ssh.PublicKey
	signer  ssh.Signer
	handle  execHandler

	mu   sync.Mutex
	cmds []string
}

func newLockServer(t *testing.T, handle execHandler) *lockServer {
	t.Helper()

	hostPair, err := issh.NewED25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := hostPair.Private.ToSSH()
	if err != nil {
		t.Fatal(err)
	}
	userPair, err := issh.NewED25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	userSigner, err := userPair.Private.ToSSH()
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	s := &lockServer{t: t, l: l, hostKey: hostSigner.PublicKey(), signer: userSigner, handle: handle}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil },
	}
	config.AddHostKey(hostSigner)
	go s.serve(config)
	return s
}

func (s *lockServer) Host() *Host {
	addr, ok := s.l.Addr().(*net.TCPAddr)
	if !ok {
		s.t.Fatalf("unexpected listener address %T", s.l.Addr())
	}
	return &Host{
		Name:     "imagetest-ssh-test",
		Addr:     "127.0.0.1",
		Port:     int32(addr.Port),
		User:     "test",
		Signer:   s.signer,
		HostKeys: []ssh.PublicKey{s.hostKey},
		LockPath: "/var/lock/imagetest/gpu",
	}
}

// Commands returns the exec commands received so far.
func (s *lockServer) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func (s *lockServer) serve(config *ssh.ServerConfig) {
	for {
		c, err := s.l.Accept()
		if err != nil {
			return
		}
		go func() {
			conn, chans, reqs, err := ssh.NewServerConn(c, config)
			if err != nil {
				return
			}
			defer conn.Close()
			go ssh.DiscardRequests(reqs)
			for newCh := range chans {
				if newCh.ChannelType() != "session" {
					_ = newCh.Reject(ssh.UnknownChannelType, "unknown channel type")
					continue
				}
				ch, chReqs, err := newCh.Accept()
				if err != nil {
					return
				}
				go s.session(ch, chReqs)
			}
		}()
	}
}

func (s *lockServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		// exec payload: uint32 length, then the command.
		cmd := string(req.Payload[4:])
		s.mu.Lock()
		s.cmds = append(s.cmds, cmd)
		s.mu.Unlock()
		_ = req.Reply(true, nil)
		status := s.handle(cmd, ch)
		var payload [4]byte
		payload[3] = byte(status)
		_, _ = ch.SendRequest("exit-status", false, payload[:])
		return
	}
}

// holdUntilEOF is a lock holder that gets the lock at once: it prints the
// token, then holds until stdin closes, like cat.
func holdUntilEOF(_ string, ch ssh.Channel) uint32 {
	_, _ = fmt.Fprintln(ch, lockToken)
	_, _ = io.Copy(io.Discard, ch)
	return 0
}

// TestAcquireLockAndRelease checks that the lock session runs the flock
// command, reports the lock held once the token arrives, and that Release
// ends the holder by closing its stdin.
func TestAcquireLockAndRelease(t *testing.T) {
	released := make(chan struct{})
	srv := newLockServer(t, func(cmd string, ch ssh.Channel) uint32 {
		status := holdUntilEOF(cmd, ch)
		close(released)
		return status
	})
	h := srv.Host()

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
	srv := newLockServer(t, func(cmd string, ch ssh.Channel) uint32 {
		time.Sleep(busy) // another holder has the lock
		return holdUntilEOF(cmd, ch)
	})
	h := srv.Host()

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
	srv := newLockServer(t, func(_ string, ch ssh.Channel) uint32 {
		_, _ = fmt.Fprintln(ch.Stderr(), "sh: 1: flock: not found")
		return 127
	})
	h := srv.Host()

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
	_, err := h.AcquireLock(t.Context())
	if err == nil || !errors.Is(err, ErrConnect) || !strings.Contains(err.Error(), "connecting for lock") {
		t.Fatalf("AcquireLock() = %v, want ErrConnect", err)
	}
}
