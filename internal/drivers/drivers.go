package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/google/go-containerregistry/pkg/name"
)

type Tester interface {
	// Setup creates the driver's resources, it must be run before Run() is
	// available
	Setup(context.Context) error
	// Teardown destroys the driver's resources
	Teardown(context.Context) error
	// Run takes a container and runs it
	Run(context.Context, name.Reference) (*RunResult, error)
}

// DefaultTeardownTimeout is the backstop deadline for teardown when no explicit
// teardown timeout is configured.
const DefaultTeardownTimeout = 15 * time.Minute

// Timeouts holds parsed driver lifecycle timeouts. Zero setup means no
// driver-level setup deadline; zero teardown means DefaultTeardownTimeout.
type Timeouts struct {
	Setup    time.Duration
	Teardown time.Duration
}

// ParseTimeouts parses setup and teardown duration strings into a Timeouts
// value. Empty strings are treated as zero (setup: no deadline; teardown:
// default backstop).
func ParseTimeouts(setup, teardown string) (Timeouts, error) {
	var t Timeouts
	if setup != "" {
		d, err := time.ParseDuration(setup)
		if err != nil {
			return t, fmt.Errorf("parsing setup timeout %q: %w", setup, err)
		}
		t.Setup = d
	}
	if teardown != "" {
		d, err := time.ParseDuration(teardown)
		if err != nil {
			return t, fmt.Errorf("parsing teardown timeout %q: %w", teardown, err)
		}
		t.Teardown = d
	}
	return t, nil
}

// SetupContext returns ctx with the setup timeout applied. If Setup is
// zero, the original context is returned unchanged.
func (t Timeouts) SetupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if t.Setup > 0 {
		return context.WithTimeout(ctx, t.Setup)
	}
	return ctx, func() {}
}

// TeardownContext returns a context for teardown: detached from the parent's
// cancellation and possibly-expired deadline (so cleanup can still run) but
// always finite, so a stuck teardown can't hang forever. Uses the configured
// Teardown timeout, else DefaultTeardownTimeout. Parent values are preserved.
func (t Timeouts) TeardownContext(ctx context.Context) (context.Context, context.CancelFunc) {
	d := t.Teardown
	if d <= 0 {
		d = DefaultTeardownTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

type RunResult struct {
	Artifact *RunArtifactResult
}

type RunArtifactResult struct {
	URI      string
	Checksum string
}

func NewRunArtifactResult(ctx context.Context, rc io.ReadCloser) (*RunArtifactResult, error) {
	af, err := os.CreateTemp("", "imagetest-artifact-*")
	if err != nil {
		return nil, err
	}
	defer af.Close()

	h := sha256.New()
	mw := io.MultiWriter(af, h)

	if _, err := io.Copy(mw, rc); err != nil {
		return nil, err
	}

	u := url.URL{
		Scheme: "file",
		Path:   af.Name(),
	}

	checksum := hex.EncodeToString(h.Sum(nil))

	clog.InfoContext(ctx, "finished copying artifact",
		"checksum", checksum,
		"uri", u.String(),
	)
	return &RunArtifactResult{
		URI:      u.String(),
		Checksum: checksum,
	}, nil
}
