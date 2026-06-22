package drivers

import (
	"context"
	"testing"
	"time"
)

func TestTeardownContextAlwaysBounded(t *testing.T) {
	ctx, cancel := Timeouts{}.TeardownContext(t.Context())
	defer cancel()

	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("teardown context has no deadline; a stuck teardown could hang forever")
	}
	if got := time.Until(dl); got > DefaultTeardownTimeout+time.Minute {
		t.Errorf("deadline %s exceeds default backstop %s", got, DefaultTeardownTimeout)
	}
}

func TestTeardownContextHonorsConfigured(t *testing.T) {
	ctx, cancel := Timeouts{Teardown: 3 * time.Minute}.TeardownContext(t.Context())
	defer cancel()

	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline")
	}
	if got := time.Until(dl); got > 3*time.Minute+time.Second || got < 2*time.Minute {
		t.Errorf("deadline %s, want ~3m", got)
	}
}

func TestTeardownContextDetachesFromParentCancellation(t *testing.T) {
	type ctxKey string
	const k ctxKey = "marker"
	parent, parentCancel := context.WithCancel(context.WithValue(t.Context(), k, "v"))
	parentCancel() // parent already cancelled, as it would be after a test timeout

	ctx, cancel := Timeouts{}.TeardownContext(parent)
	defer cancel()

	if err := ctx.Err(); err != nil {
		t.Errorf("teardown context inherited parent cancellation: %v", err)
	}
	if ctx.Value(k) != "v" {
		t.Error("teardown context dropped parent values (logger/span would be lost)")
	}
}

func TestTeardownContextDetachesFromExpiredDeadline(t *testing.T) {
	// A parent whose deadline already passed (the common case: the test phase
	// timed out) must not poison teardown — it gets a fresh finite deadline.
	parent, parentCancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer parentCancel()
	time.Sleep(time.Millisecond) // ensure the parent deadline has elapsed

	ctx, cancel := Timeouts{}.TeardownContext(parent)
	defer cancel()

	if err := ctx.Err(); err != nil {
		t.Errorf("teardown context inherited the expired parent deadline: %v", err)
	}
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) <= 0 {
		t.Errorf("teardown context has no future deadline (ok=%v)", ok)
	}
}
