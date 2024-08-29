package docker

import (
	"context"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/stretchr/testify/require"
)

func TestDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	ctx := context.Background()

	d, err := New()
	require.NoError(t, err)
	require.NotNil(t, d)

	// Create the harness
	err = d.Create(ctx)
	require.NoError(t, err)

	// Ensure we can use docker
	err = d.Run(ctx, harness.Command{
		Args: "docker run --rm hello-world",
	})
	require.NoError(t, err)

	// Ensure we can start a container and hit it via localhost
	err = d.Run(ctx, harness.Command{
		Args: "docker run -d --rm -p 8080:80 nginx && apk add curl && curl -v http://localhost:8080",
	})
	require.NoError(t, err)

	// Run a command that should fail
	err = d.Run(ctx, harness.Command{
		Args: "exit 1",
	})
	require.ErrorContains(t, err, "exit 1")
}
