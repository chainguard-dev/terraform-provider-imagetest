package pod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestMonitor(t *testing.T) {
	tests := []struct {
		name            string
		pod             *corev1.Pod
		podEvents       []watch.Event
		k8sEvents       []watch.Event
		expectError     bool
		expectedError   string
		expectedExitErr *PodMonitorError
		mockLogContent  string
	}{
		{
			name: "successful_completion",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
				},
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name: SandboxContainerName,
									State: corev1.ContainerState{
										Terminated: &corev1.ContainerStateTerminated{
											ExitCode: 0,
											Reason:   "Completed",
										},
									},
								},
							},
						},
					},
				},
			},
			k8sEvents:   []watch.Event{},
			expectError: false,
		},
		{
			name: "paused_container",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
				},
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name: SandboxContainerName,
									State: corev1.ContainerState{
										Terminated: &corev1.ContainerStateTerminated{
											ExitCode: entrypoint.ProcessPausedCode,
											Reason:   "Completed",
										},
									},
								},
							},
						},
					},
				},
			},
			k8sEvents:   []watch.Event{},
			expectError: false,
		},
		{
			name: "container_failure",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
				},
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name: SandboxContainerName,
									State: corev1.ContainerState{
										Terminated: &corev1.ContainerStateTerminated{
											ExitCode: 1,
											Reason:   "Error",
										},
									},
								},
							},
						},
					},
				},
			},
			k8sEvents:   []watch.Event{},
			expectError: true,
			expectedExitErr: &PodMonitorError{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Reason:    "container sandbox terminated: Error",
				ExitCode:  1,
			},
		},
		{
			name: "container_failure_with_logs",
			pod: &corev1.Pod{
				Name:      "test-pod-with-logs",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod-with-logs",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
				},
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod-with-logs",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name: SandboxContainerName,
									State: corev1.ContainerState{
										Terminated: &corev1.ContainerStateTerminated{
											ExitCode: 2,
											Reason:   "Error",
										},
									},
								},
							},
						},
					},
				},
			},
			k8sEvents:   []watch.Event{},
			expectError: true,
			expectedExitErr: &PodMonitorError{
				Name:      "test-pod-with-logs",
				Namespace: "test-namespace",
				Reason:    "container sandbox terminated: Error",
				ExitCode:  2,
			},
			mockLogContent: "This is a test log line\nError: command failed with exit code 2\nSome more debug information",
		},
		{
			name: "pod_deleted",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Deleted,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
					},
				},
			},
			k8sEvents:     []watch.Event{},
			expectError:   true,
			expectedError: "pod was deleted before tests could run",
		},
		{
			name: "readiness_probe_failure_as_event",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
				},
			},
			k8sEvents: []watch.Event{
				{
					Type: watch.Added,
					Object: &corev1.Event{
						Name:   "test-event",
						Reason: string(corev1.ResourceHealthStatusUnhealthy),
						Message: func() string {
							msg := struct {
								ExitCode int64  `json:"exit_code"`
								Msg      string `json:"msg"`
							}{
								ExitCode: 2,
								Msg:      "test failure",
							}
							jsonMsg, _ := json.Marshal(msg)
							return "Readiness probe failed: " + string(jsonMsg)
						}(),
					},
				},
			},
			expectError: true,
			expectedExitErr: &PodMonitorError{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Reason:    string(corev1.ResourceHealthStatusUnhealthy),
				ExitCode:  2,
			},
		},
		{
			name: "readiness_probe_pause_as_event",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
				},
			},
			k8sEvents: []watch.Event{
				{
					Type: watch.Added,
					Object: &corev1.Event{
						Name:   "test-event",
						Reason: string(corev1.ResourceHealthStatusUnhealthy),
						Message: func() string {
							msg := struct {
								ExitCode int64  `json:"exit_code"`
								Msg      string `json:"msg"`
							}{
								ExitCode: entrypoint.ProcessPausedCode,
								Msg:      "test pause",
							}
							jsonMsg, _ := json.Marshal(msg)
							return "Readiness probe failed: " + string(jsonMsg)
						}(),
					},
				},
			},
			expectError: false,
		},
		{
			name: "readiness_probe_without_exit_code_defers_to_pod_status",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents: []watch.Event{
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
				},
				{
					Type: watch.Modified,
					Object: &corev1.Pod{
						Name:      "test-pod",
						Namespace: "test-namespace",
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name: SandboxContainerName,
									State: corev1.ContainerState{
										Terminated: &corev1.ContainerStateTerminated{
											ExitCode: 0,
											Reason:   "Completed",
										},
									},
								},
							},
						},
					},
				},
			},
			k8sEvents: []watch.Event{
				{
					Type: watch.Added,
					Object: &corev1.Event{
						Name:   "test-event",
						Reason: string(corev1.ResourceHealthStatusUnhealthy),
						// Simulates a healthcheck that couldn't connect to the
						// health socket (no exit_code field). The monitor should
						// ignore this and defer to the pod status watcher.
						Message: `Readiness probe failed: {"level":"ERROR","msg":"failed to connect to health socket: dial unix /tmp/imagetest.health.sock: connect: no such file or directory"}`,
					},
				},
			},
			expectError: false,
		},
		{
			name: "context_cancelled",
			pod: &corev1.Pod{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			podEvents:   []watch.Event{},
			k8sEvents:   []watch.Event{},
			expectError: true,
			expectedExitErr: &PodMonitorError{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Reason:    "context cancelled: context canceled",
				ExitCode:  -1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a context that will be canceled if this is the "context_cancelled" test
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.name == "context_cancelled" {
				cancel()
			}

			// Create a fake clientset
			client := fake.NewClientset()

			// Set up fake watchers
			podWatcher := watch.NewFakeWithChanSize(10, false)
			k8sEventWatcher := watch.NewFakeWithChanSize(10, false)

			// Create a fake watcher for pods
			client.PrependWatchReactor("pods", func(action ktesting.Action) (handled bool, ret watch.Interface, err error) {
				return true, podWatcher, nil
			})

			// Create a fake watcher for events
			client.PrependWatchReactor("events", func(action ktesting.Action) (handled bool, ret watch.Interface, err error) {
				return true, k8sEventWatcher, nil
			})

			// Send the events to the watchers in a goroutine
			go func() {
				// Skip sending events for the "context_cancelled" test
				if tt.name == "context_cancelled" {
					return
				}

				// Give monitor time to start watching
				time.Sleep(10 * time.Millisecond)

				// Send pod events
				for _, event := range tt.podEvents {
					podWatcher.Action(event.Type, event.Object)
				}

				// Send k8s events
				for _, event := range tt.k8sEvents {
					k8sEventWatcher.Action(event.Type, event.Object)
				}
			}()

			err := monitor(ctx, client, tt.pod)

			// Check the result
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}

				if tt.expectedError != "" {
					if !strings.Contains(err.Error(), tt.expectedError) {
						t.Errorf("expected error to contain %q, got %q", tt.expectedError, err.Error())
					}
				}

				if tt.expectedExitErr != nil {
					if podErr, ok := errors.AsType[PodMonitorError](err); ok {
						if diff := cmp.Diff(tt.expectedExitErr.Name, podErr.Name); diff != "" {
							t.Errorf("Name mismatch (-want +got):\n%s", diff)
						}
						if diff := cmp.Diff(tt.expectedExitErr.Namespace, podErr.Namespace); diff != "" {
							t.Errorf("Namespace mismatch (-want +got):\n%s", diff)
						}
						if diff := cmp.Diff(tt.expectedExitErr.Reason, podErr.Reason); diff != "" {
							t.Errorf("Reason mismatch (-want +got):\n%s", diff)
						}
						if diff := cmp.Diff(tt.expectedExitErr.ExitCode, podErr.ExitCode); diff != "" {
							t.Errorf("ExitCode mismatch (-want +got):\n%s", diff)
						}
						// We don't check Logs exactly since they're implementation-dependent
						if podErr.Logs == "" {
							t.Error("Expected non-empty logs but got empty string")
						}
					} else {
						t.Errorf("Expected error to be PodMonitorError, but was %T", err)
					}
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// watchSeq returns a watch reactor that hands out the given watchers in
// order, one per watch call, then fails any further watch calls. A nil
// entry fails that call, injecting a transient watch failure.
func watchSeq(watchers ...watch.Interface) ktesting.WatchReactionFunc {
	calls := 0
	return func(ktesting.Action) (bool, watch.Interface, error) {
		calls++
		if calls <= len(watchers) && watchers[calls-1] != nil {
			return true, watchers[calls-1], nil
		}
		return true, nil, errors.New("watch dropped")
	}
}

func newWatcher() *watch.FakeWatcher {
	return watch.NewFakeWithChanSize(10, false)
}

// monitorTestClient returns a deadline-bounded context and a clientset
// seeded with objs, with a default healthy events watch already registered.
func monitorTestClient(t *testing.T, objs ...runtime.Object) (context.Context, *fake.Clientset) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	client := fake.NewClientset(objs...)
	client.PrependWatchReactor("events", watchSeq(newWatcher()))
	return ctx, client
}

// TestMonitorSurvivesWatchClosure covers watch streams ending mid-run (the
// API server times out watch requests by design, and intermediaries such as
// load balancers can additionally cut idle streams): the monitor must
// re-establish dropped watches and, failing that, still detect termination
// through the poll fallback instead of spinning on the closed channel until
// the context deadline.
func TestMonitorSurvivesWatchClosure(t *testing.T) {
	runningPod := func(name string) *corev1.Pod {
		return &corev1.Pod{
			Name:      name,
			Namespace: "test-namespace",
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		}
	}

	terminatedPod := func(name string, exitCode int32) *corev1.Pod {
		return &corev1.Pod{
			Name:      name,
			Namespace: "test-namespace",
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: SandboxContainerName,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: exitCode,
								Reason:   "Error",
							},
						},
					},
				},
			},
		}
	}

	t.Run("pod_watch_closes_then_rewatch_sees_success", func(t *testing.T) {
		pod := runningPod("test-pod")
		ctx, client := monitorTestClient(t, pod)

		firstWatcher := newWatcher()
		secondWatcher := newWatcher()
		client.PrependWatchReactor("pods", watchSeq(firstWatcher, secondWatcher))

		go func() {
			time.Sleep(10 * time.Millisecond)
			firstWatcher.Action(watch.Modified, runningPod("test-pod"))

			// Simulate the API server dropping the watch stream, then
			// deliver the successful termination only on the re-established
			// watch.
			time.Sleep(10 * time.Millisecond)
			firstWatcher.Stop()
			secondWatcher.Action(watch.Modified, terminatedPod("test-pod", 0))
		}()

		// A long poll interval ensures the re-watch path is what detects the
		// termination, not the poll fallback.
		if err := monitorWithPollInterval(ctx, client, pod, time.Hour); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("failed_rewatch_is_retried_on_poll_tick", func(t *testing.T) {
		pod := runningPod("test-pod")
		ctx, client := monitorTestClient(t, pod)

		// The immediate re-watch after the drop fails (nil entry), and the
		// poll cannot decide either (the seeded pod stays running), so the
		// only way to observe the termination on the third watcher is the
		// tick-driven watch retry.
		firstWatcher := newWatcher()
		thirdWatcher := newWatcher()
		thirdWatcher.Action(watch.Modified, terminatedPod("test-pod", 0))
		client.PrependWatchReactor("pods", watchSeq(firstWatcher, nil, thirdWatcher))
		time.AfterFunc(10*time.Millisecond, firstWatcher.Stop)

		if err := monitorWithPollInterval(ctx, client, pod, 20*time.Millisecond); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("pod_watch_closes_then_poll_sees_nonzero_exit", func(t *testing.T) {
		pod := terminatedPod("test-pod", 42)
		ctx, client := monitorTestClient(t, pod)

		// Re-watch attempts fail, leaving the poll fallback as the only way
		// to observe the termination.
		firstWatcher := newWatcher()
		client.PrependWatchReactor("pods", watchSeq(firstWatcher))
		time.AfterFunc(10*time.Millisecond, firstWatcher.Stop)

		err := monitorWithPollInterval(ctx, client, pod, 20*time.Millisecond)
		if err == nil {
			t.Fatal("expected error but got nil")
		}

		podErr, ok := errors.AsType[PodMonitorError](err)
		if !ok {
			t.Fatalf("expected error to be PodMonitorError, but was %T: %v", err, err)
		}
		if podErr.ExitCode != 42 {
			t.Errorf("expected exit code 42, got %d", podErr.ExitCode)
		}
	})

	t.Run("pod_watch_closes_then_poll_sees_deleted", func(t *testing.T) {
		pod := runningPod("test-pod")

		// The pod is not seeded into the clientset, so the poll fallback
		// observes it as deleted.
		ctx, client := monitorTestClient(t)

		firstWatcher := newWatcher()
		client.PrependWatchReactor("pods", watchSeq(firstWatcher))
		time.AfterFunc(10*time.Millisecond, firstWatcher.Stop)

		err := monitorWithPollInterval(ctx, client, pod, 20*time.Millisecond)
		if err == nil {
			t.Fatal("expected error but got nil")
		}
		if !strings.Contains(err.Error(), "pod was deleted before tests could run") {
			t.Errorf("expected pod-deleted error, got %q", err.Error())
		}
	})

	t.Run("events_watch_closes_then_rewatch_sees_pause", func(t *testing.T) {
		pod := runningPod("test-pod")
		ctx, client := monitorTestClient(t, pod)

		podWatcher := newWatcher()
		firstEventWatcher := newWatcher()
		secondEventWatcher := newWatcher()
		client.PrependWatchReactor("pods", watchSeq(podWatcher))
		// Overrides the default healthy events watch from monitorTestClient:
		// the most recently prepended reactor wins.
		client.PrependWatchReactor("events", watchSeq(firstEventWatcher, secondEventWatcher))

		go func() {
			time.Sleep(10 * time.Millisecond)
			podWatcher.Action(watch.Modified, runningPod("test-pod"))

			// Drop the events watch, then deliver the PAUSE readiness-probe
			// event only on the re-established watch.
			time.Sleep(10 * time.Millisecond)
			firstEventWatcher.Stop()

			msg := struct {
				ExitCode int64  `json:"exit_code"`
				Msg      string `json:"msg"`
			}{
				ExitCode: entrypoint.ProcessPausedCode,
				Msg:      "test pause",
			}
			jsonMsg, _ := json.Marshal(msg)
			secondEventWatcher.Action(watch.Added, &corev1.Event{
				Name:    "test-event",
				Reason:  string(corev1.ResourceHealthStatusUnhealthy),
				Message: "Readiness probe failed: " + string(jsonMsg),
			})
		}()

		if err := monitorWithPollInterval(ctx, client, pod, time.Hour); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// No mocks defined here as they aren't needed for the current tests

// TestPodMonitorErrorLogs tests that logs are properly included in error messages.
func TestPodMonitorErrorLogs(t *testing.T) {
	// Define test values
	podName := "test-pod"
	namespace := "test-namespace"
	exitCode := 1
	reason := "Error"
	testLogContent := "ERROR: Command failed with exit code 1\nLine 1 of log output\nLine 2 of log output\nTraceback information"

	// Create the PodMonitorError directly - we're testing the Error() method
	err := PodMonitorError{
		Name:      podName,
		Namespace: namespace,
		Reason:    fmt.Sprintf("container %s terminated: %s", SandboxContainerName, reason),
		ExitCode:  exitCode,
		Logs:      testLogContent,
	}

	// Test that the log content is included in the error message
	errMsg := err.Error()

	if !strings.Contains(errMsg, testLogContent) {
		t.Errorf("Expected error message to contain log content, but it did not\nError message: %s\nLog content: %s", errMsg, testLogContent)
	}

	// Verify the format of the error message
	expectedParts := []string{
		fmt.Sprintf("pod %s/%s error: %s", namespace, podName, err.Reason),
		fmt.Sprintf("exit_code=%d", exitCode),
		"Pod Logs:",
	}

	for _, part := range expectedParts {
		if !strings.Contains(errMsg, part) {
			t.Errorf("Expected error message to contain %q, but it did not\nActual: %s", part, errMsg)
		}
	}
}
