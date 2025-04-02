package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/google/go-containerregistry/pkg/name"
)

const (
	// LogAttributeKey is the key where log lines from drivers will be surfaced.
	LogAttributeKey = "driver_log"
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
