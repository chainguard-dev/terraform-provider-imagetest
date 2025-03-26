package docker

import (
	"io"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/stretchr/testify/require"
)

func TestDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	ctx := t.Context()

	d, err := New()
	require.NoError(t, err)
	require.NotNil(t, d)

	// Create a network
	nw, err := d.CreateNetwork(ctx, &NetworkRequest{})
	require.NoError(t, err)
	require.NotNil(t, nw)

	// Remove the network
	err = d.RemoveNetwork(ctx, nw)
	require.NoError(t, err)

	// Validate the network was removed
	_, err = d.cli.NetworkInspect(ctx, nw.ID, network.InspectOptions{})
	require.ErrorContains(t, err, "not found")

	// Create a network
	nw, err = d.CreateNetwork(ctx, &NetworkRequest{})
	require.NoError(t, err)
	require.NotNil(t, nw)

	// Create a managed volume
	vol, err := d.CreateVolume(ctx, &VolumeRequest{
		Target: t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, vol)

	// Run a container in the network
	resp, err := d.Start(ctx, &Request{
		Ref:        name.MustParseReference("cgr.dev/chainguard/wolfi-base:latest"),
		Entrypoint: []string{"sh"},
		Cmd:        []string{"-c", "sleep inf"},
		Mounts:     []mount.Mount{vol},
		Contents: []*Content{
			NewContentFromString("test1", "/test"),
			NewContentFromString("test2", "/tmp/test"),
			NewContentFromString("test3", "/doesnt/exist/tmp/test"),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Run a command that passes in the container
	err = resp.Run(ctx, harness.Command{Args: "exit 0"})
	require.NoError(t, err)

	// Run a command that fails in the container
	err = resp.Run(ctx, harness.Command{Args: "exit 1"})
	require.ErrorContains(t, err, "exit code 1")

	// Ensure the files were created
	err = resp.Run(ctx, harness.Command{Args: "cat /test | grep test1"})
	require.NoError(t, err)

	err = resp.Run(ctx, harness.Command{Args: "cat /tmp/test | grep test2"})
	require.NoError(t, err)

	err = resp.Run(ctx, harness.Command{Args: "cat /doesnt/exist/tmp/test | grep test3"})
	require.NoError(t, err)

	// Get a file from the container
	rc, err := resp.GetFile(ctx, "/test")
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	require.Equal(t, "test1", string(data))

	// Fail to get a file that doesn't exist from the container
	_, err = resp.GetFile(ctx, "/really/doesnt/exist/tmp/test")
	require.ErrorContains(t, err, "not find the file")

	// Fail to get a relative file from the container
	_, err = resp.GetFile(ctx, "test")
	require.ErrorContains(t, err, "not absolute")

	// Cleanup
	err = d.Remove(ctx, resp)
	require.NoError(t, err)

	err = d.RemoveVolume(ctx, vol)
	require.NoError(t, err)

	// Ensure the volume was removed
	_, err = d.cli.VolumeInspect(ctx, vol.Source)
	require.ErrorContains(t, err, "no such volume")

	err = d.RemoveNetwork(ctx, nw)
	require.NoError(t, err)
}
