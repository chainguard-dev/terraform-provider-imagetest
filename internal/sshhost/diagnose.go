package sshhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	issh "github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/kballard/go-shellquote"
	"golang.org/x/crypto/ssh"
)

// onFailureCommandTimeout bounds each on_failure command. It's longer than a
// test's on_failure timeout (10s) because host logs can be large.
const onFailureCommandTimeout = time.Minute

// errOnFailureTimeout reports that a command was stopped by its timeout.
var errOnFailureTimeout = fmt.Errorf("timed out after %s", onFailureCommandTimeout)

// remoteExec runs one on_failure command on the host.
type remoteExec func(stdout, stderr io.Writer, cmd string) error

// Diagnose runs OnFailure on the host and writes the output to w, in the
// format of a test's on_failure log. It implements the host side of
// drivers.Diagnoser; the driver decides whether the host exists yet.
func (h *Host) Diagnose(ctx context.Context, w io.Writer) error {
	if len(h.OnFailure) == 0 {
		return nil
	}

	// The budget is enforced by closing the connection when ctx ends, so
	// nothing is written to w after this returns.
	conn, done, err := h.connect(ctx)
	if err != nil {
		return fmt.Errorf("connecting to host: %w", err)
	}
	defer done()

	runOnFailure(ctx, func(stdout, stderr io.Writer, cmd string) error {
		err := issh.ExecIn(conn, h.Shell, stdout, stderr, onFailureScript(h.Shell, h.Env, cmd)...)
		if exitErr, ok := errors.AsType[*ssh.ExitError](err); ok && exitErr.ExitStatus() == 124 {
			return errOnFailureTimeout
		}
		return err
	}, h.OnFailure, w)
	return nil
}

// onFailureScript returns the lines piped into shell to run cmd: the same env
// exports as setup_commands (but not cmdStdOpts: these are best-effort), then
// cmd wrapped in timeout. timeout signals cmd's whole process group, and sudo
// passes signals on, so commands under sudo are stopped too.
func onFailureScript(shell string, env map[string]string, cmd string) []string {
	return append(envExports(env), fmt.Sprintf("timeout -k 5s %ds %s -c %s",
		int(onFailureCommandTimeout.Seconds()), shell, shellquote.Join(cmd)))
}

// runOnFailure runs each command and writes its output to w, in the same
// format as a test's on_failure log. A failing or timed-out command is
// recorded and doesn't stop the rest.
func runOnFailure(ctx context.Context, exec remoteExec, cmds []string, w io.Writer) {
	// An SSH session writes stdout and stderr concurrently.
	lw := &lockedWriter{w: w}
	for _, cmd := range cmds {
		_, _ = fmt.Fprintf(lw, "$ %s\n", cmd)
		if err := ctx.Err(); err != nil {
			_, _ = fmt.Fprintf(lw, "[skipped: %v]\n\n", err)
			continue
		}
		switch err := exec(lw, lw, cmd); {
		case errors.Is(err, errOnFailureTimeout):
			_, _ = fmt.Fprintf(lw, "[%v]\n", err)
		case err != nil:
			_, _ = fmt.Fprintf(lw, "[exit: %v]\n", err)
		}
		_, _ = fmt.Fprintln(lw)
	}
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
