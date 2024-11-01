package k3s

import (
	"context"
	"testing"
	"time"

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
	require.Contains(t, err.Error(), "exit code 1")

	// Destroy the harness
	err = h.Destroy(ctx)
	require.NoError(t, err)
}

func TestK3sSandboxInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	ctx := context.Background()

	h, err := New()
	require.NoError(t, err)

	// Create the harness
	err = h.Create(ctx)
	require.NoError(t, err)

	// Sleep for a while to let coredns stand up.
	time.Sleep(20 * time.Second)

	// We have to run this in another goroutine because we do not want to block
	// our test!
	go func() {
		err := h.Run(ctx, harness.Command{
			Args: "kubectl port-forward -n kube-system deploy/coredns 80:80",
		})
		require.NoError(t, err)
	}()
	time.Sleep(5 * time.Second)

	// Destroy the harness
	err = h.Destroy(ctx)
	require.NoError(t, err)
}
