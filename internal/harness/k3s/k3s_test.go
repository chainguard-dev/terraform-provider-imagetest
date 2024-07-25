package k3s

import (
	"context"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/stretchr/testify/require"
)

func TestK3s(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	ctx := context.Background()

	h, err := New()
	require.NoError(t, err)

	// Create the harness
	err = h.Create(ctx)
	require.NoError(t, err)

	// Ensure we can use kubectl
	err = h.Run(ctx, harness.Command{
		Args: "kubectl get po -A",
	})
	require.NoError(t, err)

	// Run a command that should fail
	err = h.Run(ctx, harness.Command{
		Args: "exit 1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "command exited with non-zero exit code: 1")

	// Destroy the harness
	err = h.Destroy(ctx)
	require.NoError(t, err)
}
