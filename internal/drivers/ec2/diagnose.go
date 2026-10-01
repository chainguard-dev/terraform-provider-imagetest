package ec2

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

// remoteExec runs one on_failure command on the instance.
type remoteExec func(stdout, stderr io.Writer, cmd string) error

// Diagnose implements drivers.Diagnoser by running the configured on_failure
// commands on the instance.
func (d *driver) Diagnose(ctx context.Context, w io.Writer) error {
	if len(d.cfg.OnFailure) == 0 {
		return nil
	}

	// Setup may have failed before the instance or the SSH key existed.
	var ip string
	switch {
	case d.cfg.ExistingInstance != nil:
		if d.existingSigner != nil {
			ip = d.existingIP
		}
	case d.instance != nil:
		ip = d.instance.publicIP
	}
	if ip == "" {
		return errors.New("instance was not created or never became reachable")
	}

	signer, err := d.sshSigner()
	if err != nil {
		return fmt.Errorf("getting SSH signer: %w", err)
	}

	conn, err := issh.Connect(ip, uint16(d.cfg.SSHPort), d.cfg.SSHUser, signer)
	if err != nil {
		return fmt.Errorf("connecting to instance: %w", err)
	}
	defer conn.Close()

	// Enforce the overall budget by closing the connection: that unblocks a
	// running session, whose Wait returns only after its output is copied, so
	// nothing is written to w after this returns.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	runOnFailure(ctx, func(stdout, stderr io.Writer, cmd string) error {
		err := issh.ExecIn(conn, d.cfg.Shell, stdout, stderr, onFailureScript(d.cfg.Shell, d.cfg.Env, cmd)...)
		if exitErr, ok := errors.AsType[*ssh.ExitError](err); ok && exitErr.ExitStatus() == 124 {
			return errOnFailureTimeout
		}
		return err
	}, d.cfg.OnFailure, w)
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
