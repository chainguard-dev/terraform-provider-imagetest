package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTester struct{}

func (fakeTester) Setup(context.Context) error    { return nil }
func (fakeTester) Teardown(context.Context) error { return nil }
func (fakeTester) Run(context.Context, name.Reference) (*drivers.RunResult, error) {
	return nil, nil
}

// fakeDiagnoser writes out to w, then returns err.
type fakeDiagnoser struct {
	fakeTester
	out string
	err error
}

func (f *fakeDiagnoser) Diagnose(_ context.Context, w io.Writer) error {
	_, _ = io.WriteString(w, f.out)
	return f.err
}

func TestDiagnose(t *testing.T) {
	data := &TestsResourceModel{Driver: "ec2"}
	tr := &TestsResource{}

	t.Run("unsupported driver is silent", func(t *testing.T) {
		assert.Nil(t, tr.diagnose(t.Context(), fakeTester{}, data))
	})

	t.Run("no output is silent", func(t *testing.T) {
		assert.Nil(t, tr.diagnose(t.Context(), &fakeDiagnoser{}, data))
	})

	t.Run("output is reported as a warning", func(t *testing.T) {
		d := tr.diagnose(t.Context(), &fakeDiagnoser{out: "$ uptime\nup 5 min\n\n"}, data)
		require.NotNil(t, d)
		assert.Equal(t, diag.SeverityWarning, d.Severity())
		assert.Equal(t, "ec2 on_failure output", d.Summary())
		assert.Equal(t, "$ uptime\nup 5 min", d.Detail(), "no trailing blank lines")
	})

	t.Run("failure warns with the output so far", func(t *testing.T) {
		d := tr.diagnose(t.Context(), &fakeDiagnoser{out: "$ uptime\nup 5 min\n", err: errors.New("connection lost")}, data)
		require.NotNil(t, d)
		assert.Equal(t, diag.SeverityWarning, d.Severity())
		assert.Equal(t, "ec2 on_failure failed", d.Summary())
		assert.True(t, strings.HasPrefix(d.Detail(), "connection lost\n\n$ uptime"), d.Detail())
	})

	t.Run("failure without output", func(t *testing.T) {
		d := tr.diagnose(t.Context(), &fakeDiagnoser{err: errors.New("unreachable")}, data)
		require.NotNil(t, d)
		assert.Equal(t, "unreachable", d.Detail())
	})

	t.Run("output up to the cap is kept whole", func(t *testing.T) {
		out := strings.Repeat("line of output\n", (maxOnFailureOutputBytes-1024)/15)
		d := tr.diagnose(t.Context(), &fakeDiagnoser{out: out}, data)
		require.NotNil(t, d)
		assert.Equal(t, strings.TrimRight(out, "\n"), d.Detail())
	})

	t.Run("output over the cap is truncated with a marker", func(t *testing.T) {
		d := tr.diagnose(t.Context(), &fakeDiagnoser{out: strings.Repeat("x", maxOnFailureOutputBytes+10)}, data)
		require.NotNil(t, d)
		assert.True(t, strings.HasSuffix(d.Detail(), fmt.Sprintf("--- output truncated (%d bytes total) ---", maxOnFailureOutputBytes+10)))
		// Leave room under gRPC's 4 MiB response limit for the test error.
		assert.Less(t, len(d.Detail()), 4<<20-2*maxErrorMessageBytes)
	})
}

func TestCapWriter(t *testing.T) {
	c := &capWriter{max: 5}
	for _, s := range []string{"abc", "defg", "hij"} {
		n, err := io.WriteString(c, s)
		require.NoError(t, err)
		assert.Equal(t, len(s), n, "must report full writes so the driver keeps draining")
	}
	assert.Equal(t, "abcde", string(c.buf))
	assert.Equal(t, int64(10), c.total)
}

func TestDiagnoseBoundsMemory(t *testing.T) {
	// A driver writing far more than the cap, in chunks.
	chunk := strings.Repeat("y", 64<<10)
	dg := &chunkDiagnoser{chunk: chunk, n: 3 * maxOnFailureOutputBytes / len(chunk)}
	d := (&TestsResource{}).diagnose(t.Context(), dg, &TestsResourceModel{Driver: "ec2"})
	require.NotNil(t, d)
	assert.Contains(t, d.Detail(), fmt.Sprintf("--- output truncated (%d bytes total) ---", 3*maxOnFailureOutputBytes))
	assert.Less(t, len(d.Detail()), maxOnFailureOutputBytes+100)
}

type chunkDiagnoser struct {
	fakeTester
	chunk string
	n     int
}

func (c *chunkDiagnoser) Diagnose(_ context.Context, w io.Writer) error {
	for range c.n {
		_, _ = io.WriteString(w, c.chunk)
	}
	return nil
}
