package ec2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExec records the commands it's asked to run and replies from tables
// keyed by command.
type fakeExec struct {
	cmds   []string
	stdout map[string]string
	errs   map[string]error
}

func (f *fakeExec) exec(stdout, stderr io.Writer, cmd string) error {
	f.cmds = append(f.cmds, cmd)
	_, _ = io.WriteString(stdout, f.stdout[cmd])
	if err := f.errs[cmd]; err != nil {
		_, _ = io.WriteString(stderr, "boom\n")
		return err
	}
	return nil
}

func TestRunOnFailure(t *testing.T) {
	t.Run("runs every command in order, in the on_failure log format", func(t *testing.T) {
		f := &fakeExec{
			stdout: map[string]string{"first": "one\n", "fourth": "four\n"},
			errs: map[string]error{
				"second": errors.New("exit 1"),
				"third":  errOnFailureTimeout,
			},
		}
		var out bytes.Buffer
		runOnFailure(t.Context(), f.exec, []string{"first", "second", "third", "fourth"}, &out)

		assert.Equal(t, []string{"first", "second", "third", "fourth"}, f.cmds, "a failing command must not stop the rest")
		assert.Equal(t, "$ first\none\n\n"+
			"$ second\nboom\n[exit: exit 1]\n\n"+
			"$ third\nboom\n[timed out after 1m0s]\n\n"+
			"$ fourth\nfour\n\n", out.String())
	})

	t.Run("expired budget skips the rest", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		f := &fakeExec{}
		var out bytes.Buffer
		runOnFailure(ctx, f.exec, []string{"first", "second"}, &out)

		assert.Empty(t, f.cmds)
		assert.Equal(t, "$ first\n[skipped: context canceled]\n\n$ second\n[skipped: context canceled]\n\n", out.String())
	})
}

func TestOnFailureScript(t *testing.T) {
	got := onFailureScript("bash", map[string]string{"B": "two words", "A": "1"}, `cat "$LOG_DIR"/x.log`)
	assert.Equal(t, []string{
		"export A=1",
		`export B='two words'`,
		`timeout -k 5s 60s bash -c 'cat "$LOG_DIR"/x.log'`,
	}, got)
}

// TestOnFailureScriptRealShell pipes the script into a local shell, as the
// driver does over SSH, to check quoting and env inheritance through timeout.
func TestOnFailureScriptRealShell(t *testing.T) {
	for _, tool := range []string{"bash", "timeout"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s", tool)
		}
	}
	run := func(cmd string) (string, error) {
		c := exec.Command("bash")
		c.Stdin = strings.NewReader(strings.Join(onFailureScript("bash", map[string]string{"LOG_DIR": "/var/log/my app"}, cmd), "\n") + "\n")
		out, err := c.CombinedOutput()
		return string(out), err
	}

	out, err := run(`echo "dir=$LOG_DIR" 'it'"'"'s' | tr a-z A-Z`)
	require.NoError(t, err)
	assert.Equal(t, "DIR=/VAR/LOG/MY APP IT'S\n", out)

	_, err = run("exit 3")
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, 3, exitErr.ExitCode(), "the command's exit code must come through timeout")
}

func TestDiagnose(t *testing.T) {
	t.Run("nothing configured, nothing to do", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&driver{}).Diagnose(t.Context(), &out))
		assert.Empty(t, out.String())
	})

	cmds := []string{"uptime"}
	for name, d := range map[string]*driver{
		"new instance never created":   {cfg: Config{OnFailure: cmds}},
		"new instance never reachable": {cfg: Config{OnFailure: cmds}, instance: &instance{}},
		// Setup fails reading the key before the signer is set.
		"existing instance without key": {cfg: Config{OnFailure: cmds, ExistingInstance: &ExistingInstance{IP: "127.0.0.1", SSHKey: "/nope"}}},
	} {
		t.Run(name, func(t *testing.T) {
			err := d.Diagnose(t.Context(), io.Discard)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not created")
		})
	}
}
