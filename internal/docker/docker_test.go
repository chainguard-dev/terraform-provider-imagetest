package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
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
	_, err = d.inner.NetworkInspect(ctx, nw.ID, client.NetworkInspectOptions{})
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
	_, err = d.inner.VolumeInspect(ctx, vol.Source, client.VolumeInspectOptions{})
	require.ErrorContains(t, err, "no such volume")

	err = d.RemoveNetwork(ctx, nw)
	require.NoError(t, err)
}

// TestStartRemovesConflictingContainer ensures Start() succeeds even when a
// same-named container leaked from a previous failed bring-up attempt.
func TestStartRemovesConflictingContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	ctx := t.Context()

	d, err := New()
	require.NoError(t, err)

	const cname = "imagetest-conflict-test"
	ref := name.MustParseReference("cgr.dev/chainguard/wolfi-base:latest")

	// Simulate the corpse of a failed first attempt: a container created with
	// the target name and the imagetest ownership label, but never started or
	// cleaned up.
	require.NoError(t, d.pull(ctx, ref))
	corpse, err := d.inner.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  ref.String(),
			Labels: map[string]string{"dev.chainguard.imagetest": "true"},
		},
		Name: cname,
	})
	require.NoError(t, err)
	require.NotEmpty(t, corpse.ID)
	t.Cleanup(func() {
		_, _ = d.inner.ContainerRemove(context.Background(), cname, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	})

	// The second attempt must remove the corpse and start cleanly.
	resp, err := d.Start(ctx, &Request{
		Name:       cname,
		Ref:        ref,
		Entrypoint: []string{"sh"},
		Cmd:        []string{"-c", "sleep inf"},
	})
	require.NoError(t, err)
	require.NotEqual(t, corpse.ID, resp.ID)

	err = d.Remove(ctx, resp)
	require.NoError(t, err)
}

// TestStartRefusesUnownedConflictingContainer ensures Start() does not remove
// a conflicting container that was not created by imagetest.
func TestStartRefusesUnownedConflictingContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	ctx := t.Context()

	d, err := New()
	require.NoError(t, err)

	const cname = "imagetest-unowned-conflict-test"
	ref := name.MustParseReference("cgr.dev/chainguard/wolfi-base:latest")

	// A same-named container without the imagetest ownership label.
	require.NoError(t, d.pull(ctx, ref))
	unowned, err := d.inner.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: ref.String(),
		},
		Name: cname,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = d.inner.ContainerRemove(context.Background(), cname, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	})

	_, err = d.Start(ctx, &Request{
		Name:       cname,
		Ref:        ref,
		Entrypoint: []string{"sh"},
		Cmd:        []string{"-c", "sleep inf"},
	})
	require.ErrorContains(t, err, "not owned by imagetest")

	// The unowned container must still exist.
	inspect, err := d.inner.ContainerInspect(ctx, cname, client.ContainerInspectOptions{})
	require.NoError(t, err)
	require.Equal(t, unowned.ID, inspect.Container.ID)
}

func TestFailedCheck(t *testing.T) {
	unhealthy := func(running bool, codes ...int) *container.State {
		var log []*container.HealthcheckResult
		for _, c := range codes {
			log = append(log, &container.HealthcheckResult{ExitCode: c})
		}
		return &container.State{
			Running: running,
			Health:  &container.Health{Status: container.Unhealthy, FailingStreak: 1, Log: log},
		}
	}
	streak := func(n int, state *container.State) *container.State {
		state.Health.FailingStreak = n
		return state
	}

	for _, tc := range []struct {
		name     string
		state    *container.State
		wantCode int
		wantOK   bool
	}{
		{name: "paused", state: unhealthy(true, 0, 78), wantCode: 78, wantOK: true},
		{name: "paused with error", state: unhealthy(true, 0, 75), wantCode: 75, wantOK: true},
		{name: "probe killed by exit", state: unhealthy(true, 0, 137)},
		{name: "probe could not start", state: unhealthy(true, 137, -1)},
		{name: "health socket unreachable", state: unhealthy(true, 0, 232)},
		{name: "exit lag", state: streak(failingStreakLimit-1, unhealthy(true, -1, -1))},
		{name: "failing for too long", state: streak(failingStreakLimit, unhealthy(true, -1, 232)), wantCode: 232, wantOK: true},
		{name: "not running", state: unhealthy(false, 78)},
		{name: "empty log", state: unhealthy(true)},
		{name: "no health", state: &container.State{Running: true}},
		{name: "no state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check, ok := FailedCheck(tc.state)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.Equal(t, tc.wantCode, check.ExitCode)
			}
		})
	}
}

// TestPullRetriesErrorInStream checks that a pull whose stream reports an
// error is treated as failed and retried. The Docker API answers a pull with
// 200 and reports registry failures as messages in the stream.
func TestPullRetriesErrorInStream(t *testing.T) {
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/images/create"):
			if pulls.Add(1) == 1 {
				_, _ = fmt.Fprintln(w, `{"errorDetail":{"message":"unavailable"},"error":"unavailable"}`)
				return
			}
			_, _ = fmt.Fprintln(w, `{"status":"Downloaded newer image"}`)
		case strings.HasSuffix(r.URL.Path, "/json"):
			http.Error(w, `{"message":"No such image"}`, http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	cli, err := client.New(client.WithHost("tcp://" + srv.Listener.Addr().String()))
	require.NoError(t, err)
	d := &Client{inner: cli}

	require.NoError(t, d.pull(t.Context(), name.MustParseReference("example.com/image:latest")))
	require.Equal(t, int32(2), pulls.Load())
}
