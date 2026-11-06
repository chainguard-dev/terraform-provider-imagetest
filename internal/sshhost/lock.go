package sshhost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/kballard/go-shellquote"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"
)

// lockReleaseTimeout bounds how long Release waits for the lock holder on
// the host to exit after its stdin is closed.
const lockReleaseTimeout = 10 * time.Second

// ErrConnect marks an AcquireLock failure in the SSH connection itself,
// before anything ran on the host, so a driver can report it as it reports
// a failed first connection.
var ErrConnect = errors.New("ssh connection failed")

// Lock is an exclusive flock on a file on the host, held by a process in an
// SSH session that stays open until Release. The process dies with the
// session, so a connection that drops releases the lock without the
// driver's help.
type Lock struct {
	Path string
	// Waited is how long the host made the run wait for the lock.
	Waited time.Duration

	conn    *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	done    chan error
}

// LockToken is the line the lock holder prints once flock returns. The
// client reads stdout until it sees the line, so the wait for a busy lock
// is measured on the host and not guessed. It is exported for tests that
// play the holder.
const LockToken = "imagetest-lock-held"

// lockCommand returns the shell line that takes the lock on path and holds
// it until stdin closes. flock(1) opens path, takes an exclusive lock and
// runs the command with the open descriptor inherited; cat holds that
// descriptor until its stdin ends. Closing the session's stdin, or the
// session dying with its connection, ends cat and releases the lock.
func lockCommand(path string) string {
	return fmt.Sprintf("flock -x %s -c %s", shellquote.Join(path),
		shellquote.Join("echo "+LockToken+"; exec cat"))
}

// AcquireLock takes an exclusive flock on LockPath over a new SSH
// connection that stays open until the returned Lock is released. The wait
// for the lock is bounded by ctx; the connection and the lock are not,
// since they outlive Setup. The wait is recorded as the span event
// ssh.lock.acquired with the path and the time waited.
func (h *Host) AcquireLock(ctx context.Context) (*Lock, error) {
	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	conn, err := h.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting for lock: %w: %w", ErrConnect, err)
	}

	session, err := conn.NewSession()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("opening lock session: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("lock session stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("lock session stdout: %w", err)
	}
	stderr := new(bytes.Buffer)
	session.Stderr = stderr

	l := &Lock{Path: h.LockPath, conn: conn, session: session, stdin: stdin, done: make(chan error, 1)}

	log.Info("waiting for host lock", "path", h.LockPath)
	start := time.Now()
	if err := session.Start(lockCommand(h.LockPath)); err != nil {
		l.close()
		return nil, fmt.Errorf("starting lock holder: %w", err)
	}

	// The holder exits only when released, when flock fails, or when the
	// connection drops. Its exit is reported through done so the wait
	// below and Release both see it.
	go func() { l.done <- session.Wait() }()

	// Wait for the token, keep stdout drained afterwards so the holder is
	// never blocked on a full channel window.
	held := make(chan error, 1)
	go func() {
		r := bufio.NewReader(stdout)
		for {
			line, err := r.ReadString('\n')
			if strings.TrimSpace(line) == LockToken {
				held <- nil
				_, _ = io.Copy(io.Discard, r)
				return
			}
			if err != nil {
				held <- fmt.Errorf("lock holder ended before taking the lock: %w", err)
				return
			}
		}
	}()

	select {
	case err := <-held:
		if err != nil {
			// The holder printed nothing: flock is missing, or the path
			// cannot be opened. Its exit status and stderr say which.
			exit := <-l.done
			l.close()
			return nil, fmt.Errorf("taking lock %s: %w: %v: %s", h.LockPath, err, exit, strings.TrimSpace(stderr.String()))
		}
	case <-ctx.Done():
		l.close()
		return nil, fmt.Errorf("waiting for lock %s: %w", h.LockPath, ctx.Err())
	}

	l.Waited = time.Since(start)
	span.AddEvent("ssh.lock.acquired", trace.WithAttributes(
		attribute.String("lock.path", h.LockPath),
		attribute.Int64("lock.wait_ms", l.Waited.Milliseconds()),
	))
	log.Info("host lock acquired", "path", h.LockPath, "waited", l.Waited.Round(time.Millisecond).String())
	return l, nil
}

// Release ends the lock holder on the host and closes the connection. The
// lock is released by the holder's exit, or by the connection closing if
// the holder does not exit in time. A holder that already ended, because
// the connection dropped mid-run, has released the lock too, so it is
// logged and not an error.
func (l *Lock) Release(ctx context.Context) error {
	log := clog.FromContext(ctx)
	log.Info("releasing host lock", "path", l.Path)
	defer l.close()

	// EOF on stdin ends cat, then flock, which drops the lock.
	_ = l.stdin.Close()

	timer := time.NewTimer(lockReleaseTimeout)
	defer timer.Stop()
	select {
	case err := <-l.done:
		if err != nil {
			log.Warn("lock holder ended with an error, the lock is released all the same", "path", l.Path, "error", err)
		}
		return nil
	case <-timer.C:
		log.Warn("lock holder did not exit, closing the connection releases the lock", "path", l.Path)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Lock) close() {
	_ = l.session.Close()
	_ = l.conn.Close()
}
