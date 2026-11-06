package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

// fakeAdmitter waits for wait in Admit, as a driver queued behind a host
// lock does, and keeps the context it was given.
type fakeAdmitter struct {
	fakeTester
	wait time.Duration
	err  error

	admitCtx context.Context
}

func (f *fakeAdmitter) Admit(ctx context.Context) error {
	f.admitCtx = ctx
	select {
	case <-time.After(f.wait):
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestAdmitExtendsTestClock checks that a driver's wait in Admit runs
// outside the tests resource timeout and moves the deadline forward by
// the time waited, over every attempt, so the time left for the tests is
// the whole timeout once the driver is admitted.
func TestAdmitExtendsTestClock(t *testing.T) {
	const timeout = 500 * time.Millisecond
	tr := &TestsResource{}
	data := &TestsResourceModel{Driver: "ssh"}
	tracer := otel.Tracer("test")
	clock := newClock(t, timeout)

	// A wait twice the test timeout.
	a := &fakeAdmitter{wait: 2 * timeout}
	ctx, cancel, err := tr.admit(clock, a, data, tracer)
	require.NoError(t, err)
	defer cancel()

	_, hasDeadline := a.admitCtx.Deadline()
	assert.False(t, hasDeadline, "Admit ran under the test deadline")
	require.NoError(t, ctx.Err(), "the test context expired during the wait")
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	left := time.Until(deadline)
	assert.Greater(t, left, timeout/2, "the wait counted against the test timeout")
	assert.LessOrEqual(t, left, timeout)
	assert.NoError(t, clock.loop.Err(), "the retry loop's context expired during the wait")

	// A second attempt waits again; its deadline is the original moved
	// forward by both waits.
	b := &fakeAdmitter{wait: timeout / 2}
	ctx2, cancel2, err := tr.admit(clock, b, data, tracer)
	require.NoError(t, err)
	defer cancel2()
	deadline2, _ := ctx2.Deadline()
	assert.GreaterOrEqual(t, clock.waited, 2*timeout+timeout/2)
	assert.True(t, deadline2.After(deadline), "second attempt's deadline %s is not after the first's %s", deadline2, deadline)
	assert.Greater(t, time.Until(deadline2), timeout/2)
}

// newClock returns a clock started timeout from now, stopped with the test.
func newClock(t *testing.T, timeout time.Duration) *testClock {
	t.Helper()
	timed, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	c := newTestClock(t.Context(), timed)
	t.Cleanup(c.stop)
	return c
}

// TestClockRunsOutUnextended checks that a clock no driver extends ends
// its loop context at the resource deadline, as the resource context
// does, so drivers that do not admit keep their retry semantics.
func TestClockRunsOutUnextended(t *testing.T) {
	clock := newClock(t, 50*time.Millisecond)
	select {
	case <-clock.loop.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("loop context did not end at the deadline")
	}
}

// TestAdmitFailure checks that an Admit that fails, as it does when the
// lock wait passes its bound, returns its error and no test context, and
// leaves the clock where it was.
func TestAdmitFailure(t *testing.T) {
	tr := &TestsResource{}
	clock := newClock(t, time.Minute)

	ctx, cancel, err := tr.admit(clock, &fakeAdmitter{err: errors.New("imagetest: ssh lock wait timed out: /var/lock/gpu was not free within 1h0m0s")}, &TestsResourceModel{Driver: "ssh"}, otel.Tracer("test"))
	require.ErrorContains(t, err, "lock wait timed out")
	assert.Nil(t, ctx)
	assert.Nil(t, cancel)
	assert.Zero(t, clock.waited)
	assert.NoError(t, clock.loop.Err(), "the clock did not resume")
}

// TestAdmitFollowsResourceCancel checks that the context Admit waits under
// still ends when the resource context is cancelled: the wait is outside
// the deadline, not outside Terraform's control.
func TestAdmitFollowsResourceCancel(t *testing.T) {
	base, cancelBase := context.WithCancel(t.Context())
	timed, cancelTimed := context.WithTimeout(base, time.Minute)
	defer cancelTimed()
	clock := newTestClock(base, timed)
	defer clock.stop()
	time.AfterFunc(50*time.Millisecond, cancelBase)

	_, _, err := (&TestsResource{}).admit(clock, &fakeAdmitter{wait: 10 * time.Second}, &TestsResourceModel{Driver: "ssh"}, otel.Tracer("test"))
	assert.ErrorIs(t, err, context.Canceled)
}
