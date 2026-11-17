package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/google/go-cmp/cmp"
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

// fakeDaemon serves the subset of the Docker Engine API that Client.Run uses,
// in memory, for one container with a 1s healthcheck interval and Retries 1.
// Like dockerd, it runs a probe every interval for as long as it reports the
// container as running, logs each result with its own start time, and keeps
// the last five. Use it inside synctest so that time is virtual.
type fakeDaemon struct {
	// probe returns the result of the probe that starts at elapsed since the
	// container started.
	probe func(elapsed time.Duration) (exitCode int, output string)
	// exitAfter is when the daemon processes the container's exit: it stops
	// probing, reports the container as exited and answers the wait. Zero
	// means the container never exits.
	exitAfter time.Duration
	exitCode  int64
	// inspectFails, if set, reports whether inspecting the container at
	// elapsed fails.
	inspectFails func(elapsed time.Duration) bool
	// inspectLatency delays every container inspect.
	inspectLatency time.Duration

	started time.Time
}

const fakeContainerID = "fake"

// state returns the container state the daemon reports at elapsed.
func (f *fakeDaemon) state(elapsed time.Duration) *container.State {
	health := &container.Health{Status: container.Starting}
	for at := time.Second; at <= elapsed && (f.exitAfter == 0 || at < f.exitAfter); at += time.Second {
		exitCode, output := f.probe(at)
		start := f.started.Add(at)
		health.Log = append(health.Log, &container.HealthcheckResult{
			Start:    start,
			End:      start.Add(10 * time.Millisecond),
			ExitCode: exitCode,
			Output:   output,
		})
		if exitCode == 0 {
			health.Status, health.FailingStreak = container.Healthy, 0
		} else {
			health.Status = container.Unhealthy
			health.FailingStreak++
		}
	}
	if n := len(health.Log); n > 5 {
		health.Log = health.Log[n-5:]
	}

	if f.exitAfter > 0 && elapsed >= f.exitAfter {
		return &container.State{Status: container.StateExited, ExitCode: int(f.exitCode), Health: health}
	}
	return &container.State{Status: container.StateRunning, Running: true, Health: health}
}

func (f *fakeDaemon) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	if i := strings.Index(path[1:], "/"); strings.HasPrefix(path, "/v") && i >= 0 {
		path = path[i+1:]
	}

	respond := func(code int, body any) (*http.Response, error) {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: code,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(b)),
			Request:    req,
		}, nil
	}

	switch {
	case req.Method == http.MethodGet && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		return respond(http.StatusOK, map[string]string{"Id": "sha256:fake"})
	case req.Method == http.MethodPost && path == "/images/create":
		return respond(http.StatusOK, map[string]string{"status": "pulled"})
	case req.Method == http.MethodPost && path == "/containers/create":
		return respond(http.StatusCreated, map[string]string{"Id": fakeContainerID})
	case req.Method == http.MethodPost && path == "/containers/"+fakeContainerID+"/start":
		f.started = time.Now()
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
	case req.Method == http.MethodPost && path == "/containers/"+fakeContainerID+"/wait":
		// Headers go out immediately; the body arrives when the container
		// exits, as with the real daemon.
		pr, pw := io.Pipe()
		go func() {
			var exited <-chan time.Time
			if f.exitAfter > 0 {
				exited = time.After(f.exitAfter - time.Since(f.started))
			}
			select {
			case <-exited:
				_ = json.NewEncoder(pw).Encode(container.WaitResponse{StatusCode: f.exitCode})
				_ = pw.Close()
			case <-req.Context().Done():
				_ = pw.CloseWithError(req.Context().Err())
			}
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       pr,
			Request:    req,
		}, nil
	case req.Method == http.MethodGet && path == "/containers/"+fakeContainerID+"/json":
		select {
		case <-time.After(f.inspectLatency):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		elapsed := time.Since(f.started)
		if f.inspectFails != nil && f.inspectFails(elapsed) {
			return respond(http.StatusServiceUnavailable, map[string]string{"message": "fake daemon: unavailable"})
		}
		return respond(http.StatusOK, container.InspectResponse{ID: fakeContainerID, State: f.state(elapsed)})
	}
	return respond(http.StatusNotFound, map[string]string{"message": "fake daemon: unhandled " + req.Method + " " + path})
}

// runHealthChecked runs a health-checked container against f in a synctest
// bubble, returning Run's error and how much virtual time it took. Run is
// bounded to a few minutes of virtual time, so a watcher that never reports
// fails the test instead of hanging it.
func runHealthChecked(t *testing.T, f *fakeDaemon) (time.Duration, error) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	var (
		elapsed time.Duration
		err     error
	)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()

		inner, cerr := client.New(
			client.WithHost("tcp://fake.invalid:2375"),
			client.WithAPIVersion(client.MaxAPIVersion),
			client.WithHTTPClient(&http.Client{Transport: f}),
		)
		if cerr != nil {
			t.Fatalf("client.New() error = %v", cerr)
		}
		d, cerr := New(WithClient(inner))
		if cerr != nil {
			t.Fatalf("New() error = %v", cerr)
		}

		start := time.Now()
		_, err = d.Run(ctx, &Request{
			Ref:         name.MustParseReference("registry.invalid/test:latest"),
			HealthCheck: &container.HealthConfig{Interval: time.Second, Retries: 1},
		})
		elapsed = time.Since(start)
	})
	return elapsed, err
}

// TestRunExitWinsOverProbesRacingExit reproduces the container exit race.
// When PID 1 exits, the kernel kills the probe in flight, which the daemon
// logs as a failure. Until the daemon processes the exit it still reports the
// container as running and keeps probing it, and those probes cannot start.
// The container's own exit result must decide the outcome.
func TestRunExitWinsOverProbesRacingExit(t *testing.T) {
	const (
		exitAt = 3 * time.Second
		// How long a slow daemon keeps reporting the container as running
		// after PID 1 has exited.
		daemonLag = 45 * time.Second
	)

	for _, tc := range []struct {
		name     string
		exitCode int64
		// output of the probes that cannot start after PID 1 has exited.
		output string
		// timedOutBeforeExit makes the probe just before the killed one time
		// out, as on a slow node.
		timedOutBeforeExit bool
		want               *RunError
	}{{
		name:   "success/init process not running",
		output: "failed to create exec: container fake init process is not running",
	}, {
		name:   "success/stopped state",
		output: "OCI runtime exec failed: cannot exec in a stopped state",
	}, {
		name:               "success/timed out before exit",
		output:             "OCI runtime exec failed: cannot exec in a stopped state",
		timedOutBeforeExit: true,
	}, {
		name:     "failure",
		exitCode: 3,
		output:   "failed to create exec: container fake init process is not running",
		want:     &RunError{ExitCode: 3},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDaemon{
				exitAfter: exitAt + daemonLag,
				exitCode:  tc.exitCode,
				probe: func(at time.Duration) (int, string) {
					switch {
					case tc.timedOutBeforeExit && at == exitAt-time.Second:
						return -1, "Health check exceeded timeout (5s)"
					case at < exitAt:
						return 0, ""
					case at == exitAt:
						return 137, ""
					default:
						return -1, tc.output
					}
				},
			}

			_, err := runHealthChecked(t, f)

			if tc.want == nil {
				if err != nil {
					t.Fatalf("Run() error = %v, want nil", err)
				}
				return
			}
			got, ok := errors.AsType[*RunError](err)
			if !ok {
				t.Fatalf("Run() error = %v, want *RunError", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Run() RunError mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRunReportsUnhealthyWhileRunning ensures a container that keeps failing
// its healthcheck while it is still running is reported on the second failed
// probe, without waiting for it to exit.
func TestRunReportsUnhealthyWhileRunning(t *testing.T) {
	const failFrom = 3 * time.Second

	f := &fakeDaemon{probe: func(at time.Duration) (int, string) {
		if at < failFrom {
			return 0, ""
		}
		return 75, "paused after error"
	}}

	elapsed, err := runHealthChecked(t, f)

	got, ok := errors.AsType[*RunError](err)
	if !ok {
		t.Fatalf("Run() error = %v, want *RunError", err)
	}
	want := &RunError{ExitCode: 75, Message: "paused after error"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Run() RunError mismatch (-want +got):\n%s", diff)
	}
	if limit := failFrom + 2*time.Second; elapsed > limit {
		t.Errorf("Run() reported unhealthy after %v, want within %v", elapsed, limit)
	}
}

// TestRunToleratesIsolatedProbeFailures ensures a failed probe followed by a
// healthy one is not reported, including when inspects are slow enough that
// the watcher never observes the healthy probes as the latest result.
func TestRunToleratesIsolatedProbeFailures(t *testing.T) {
	for _, tc := range []struct {
		name           string
		inspectLatency time.Duration
		failing        func(at time.Duration) bool
	}{{
		name:    "fast inspect",
		failing: func(at time.Duration) bool { return at == 3*time.Second || at == 6*time.Second },
	}, {
		name:           "slow inspect",
		inspectLatency: 2 * time.Second,
		failing:        func(at time.Duration) bool { return at >= 3*time.Second && (at/time.Second)%2 == 1 },
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDaemon{
				exitAfter:      20 * time.Second,
				inspectLatency: tc.inspectLatency,
				probe: func(at time.Duration) (int, string) {
					if tc.failing(at) {
						return 1, "flaky"
					}
					return 0, ""
				},
			}

			if _, err := runHealthChecked(t, f); err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
		})
	}
}

// TestRunReportsPersistentInconclusiveProbes ensures an unhealthy streak
// without two consecutive probes that ran and failed, while the container
// keeps running, is reported once it has lasted inconclusiveFor, and not
// before.
func TestRunReportsPersistentInconclusiveProbes(t *testing.T) {
	const (
		failFrom   = 3 * time.Second
		timeoutOut = "Health check exceeded timeout (5s)"
	)

	for _, tc := range []struct {
		name  string
		probe func(at time.Duration) (int, string)
		want  *RunError
	}{{
		name:  "timed out",
		probe: func(time.Duration) (int, string) { return -1, timeoutOut },
		want:  &RunError{ExitCode: -1, Message: timeoutOut},
	}, {
		name: "timed out mixed with failures",
		probe: func(at time.Duration) (int, string) {
			if (at/time.Second)%5 == 0 {
				return 232, "failed to connect to health socket"
			}
			return -1, timeoutOut
		},
		want: &RunError{ExitCode: -1, Message: timeoutOut},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDaemon{probe: func(at time.Duration) (int, string) {
				if at < failFrom {
					return 0, ""
				}
				return tc.probe(at)
			}}

			elapsed, err := runHealthChecked(t, f)

			got, ok := errors.AsType[*RunError](err)
			if !ok {
				t.Fatalf("Run() error = %v, want *RunError", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Run() RunError mismatch (-want +got):\n%s", diff)
			}
			if earliest, latest := failFrom+inconclusiveFor, failFrom+inconclusiveFor+2*time.Second; elapsed < earliest || elapsed > latest {
				t.Errorf("Run() reported unhealthy after %v, want between %v and %v", elapsed, earliest, latest)
			}
		})
	}
}

// TestRunInconclusiveWindowResets ensures a healthy probe restarts the
// window for an unhealthy streak, and a successful inspect restarts the
// window for failing inspects, so two streaks that are each shorter than
// inconclusiveFor are not reported.
func TestRunInconclusiveWindowResets(t *testing.T) {
	const (
		exitAfter = 100 * time.Second
		// Each streak is shorter than inconclusiveFor, together they are
		// longer.
		streak = inconclusiveFor - 20*time.Second
		gap    = 2 * time.Second
		start  = 3 * time.Second
	)
	inFirstOrSecondStreak := func(at time.Duration) bool {
		return (at >= start && at < start+streak) || (at >= start+streak+gap && at < start+2*streak+gap)
	}
	healthy := func(time.Duration) (int, string) { return 0, "" }

	for _, tc := range []struct {
		name string
		f    *fakeDaemon
	}{{
		name: "healthy probe",
		f: &fakeDaemon{
			exitAfter: exitAfter,
			probe: func(at time.Duration) (int, string) {
				if inFirstOrSecondStreak(at) {
					return -1, "Health check exceeded timeout (5s)"
				}
				return 0, ""
			},
		},
	}, {
		name: "successful inspect",
		f: &fakeDaemon{
			exitAfter:    exitAfter,
			probe:        healthy,
			inspectFails: inFirstOrSecondStreak,
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runHealthChecked(t, tc.f); err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
		})
	}
}

// TestRunToleratesInspectErrors ensures failing container inspects only fail
// Run once they have persisted for inconclusiveFor.
func TestRunToleratesInspectErrors(t *testing.T) {
	healthy := func(time.Duration) (int, string) { return 0, "" }

	t.Run("transient", func(t *testing.T) {
		f := &fakeDaemon{
			exitAfter:    20 * time.Second,
			probe:        healthy,
			inspectFails: func(elapsed time.Duration) bool { return elapsed >= 2*time.Second && elapsed < 10*time.Second },
		}

		if _, err := runHealthChecked(t, f); err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	})

	t.Run("persistent", func(t *testing.T) {
		const failFrom = 2 * time.Second
		f := &fakeDaemon{
			probe:        healthy,
			inspectFails: func(elapsed time.Duration) bool { return elapsed >= failFrom },
		}

		elapsed, err := runHealthChecked(t, f)

		if !cerrdefs.IsUnavailable(err) {
			t.Fatalf("Run() error = %v, want the daemon's unavailable error", err)
		}
		if earliest, latest := failFrom+inconclusiveFor, failFrom+inconclusiveFor+2*time.Second; elapsed < earliest || elapsed > latest {
			t.Errorf("Run() failed after %v, want between %v and %v", elapsed, earliest, latest)
		}
	})
}
