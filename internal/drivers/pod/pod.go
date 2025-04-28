package pod

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/google/go-containerregistry/pkg/name"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1apply "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/utils/ptr"
)

const (
	SandboxContainerName  = "sandbox"
	ArtifactContainerName = "artifacts"
)

type opts struct {
	Name             string
	Namespace        string
	ImageRef         name.Reference
	WorkingDir       string
	ExtraEnvs        map[string]string
	ExtraAnnotations map[string]string
	ExtraLabels      map[string]string

	client kubernetes.Interface
	cfg    *rest.Config
}

type RunOpts func(*opts) error

func Run(ctx context.Context, kcfg *rest.Config, options ...RunOpts) (*drivers.RunResult, error) {
	o := opts{
		Name:       "imagetest",
		Namespace:  "imagetest",
		ImageRef:   name.MustParseReference("cgr.dev/chainguard/kubectl:latest-dev"),
		WorkingDir: entrypoint.DefaultWorkDir,
		ExtraLabels: map[string]string{
			"dev.chainguard.imagetest": "true",
		},
		ExtraAnnotations: map[string]string{},
		ExtraEnvs: map[string]string{
			"IMAGETEST": "true",
		},

		cfg: kcfg,
	}

	for _, opt := range options {
		if err := opt(&o); err != nil {
			return nil, err
		}
	}

	kcli, err := kubernetes.NewForConfig(kcfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}
	o.client = kcli

	if err := o.preflight(ctx); err != nil {
		return nil, err
	}

	pobj, err := o.client.CoreV1().Pods(o.Namespace).
		Create(ctx, o.pod(), metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	pw, err := o.client.CoreV1().Pods(pobj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", pobj.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch pod: %w", err)
	}
	defer pw.Stop()

	ew, err := o.client.CoreV1().Events(pobj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", pobj.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch events: %w", err)
	}
	defer ew.Stop()

	ctx = clog.WithValues(ctx,
		"pod_name", pobj.Name,
		"pod_namespace", pobj.Namespace,
	)

	if err := monitor(ctx, o.client, pobj); err != nil {
		return nil, err
	}

	result := &drivers.RunResult{Artifact: &drivers.RunArtifactResult{}}
	if err := o.getArtifact(ctx, pobj, result); err != nil {
		clog.ErrorContext(ctx, "failed to get artifact", "error", err)
	}

	return result, nil
}

func (o *opts) preflight(ctx context.Context) error {
	// validate the client has the permissions necessary
	resp, err := o.client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: o.Namespace,
				Verb:      "create",
				Group:     "apps",
				Resource:  "pods",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create authorization review: %w", err)
	}

	if !resp.Status.Allowed {
		return fmt.Errorf("user does not have permission to create pods in the %s namespace", o.Namespace)
	}

	nsa := corev1apply.Namespace(o.Namespace).WithName(o.Namespace)
	if _, err := o.client.CoreV1().Namespaces().Apply(ctx, nsa, metav1.ApplyOptions{
		FieldManager: "imagetest",
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed to apply namespace: %w", err)
	}

	// Create the relevant rbac
	saa := corev1apply.ServiceAccount(o.Name, o.Namespace).WithName(o.Name)
	if _, err := o.client.CoreV1().ServiceAccounts(o.Namespace).Apply(ctx, saa, metav1.ApplyOptions{
		FieldManager: "imagetest",
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed to apply service account: %w", err)
	}

	// Create the role binding
	crba := rbacv1apply.ClusterRoleBinding(o.Name).
		WithName(o.Name).
		WithSubjects(&rbacv1apply.SubjectApplyConfiguration{
			Kind:      ptr.To(rbacv1.ServiceAccountKind),
			Name:      ptr.To(o.Name),
			Namespace: ptr.To(o.Namespace),
		}).
		WithRoleRef(&rbacv1apply.RoleRefApplyConfiguration{
			APIGroup: ptr.To(rbacv1.GroupName),
			Kind:     ptr.To("ClusterRole"),
			Name:     ptr.To("cluster-admin"),
		})
	if _, err := o.client.RbacV1().ClusterRoleBindings().Apply(ctx, crba, metav1.ApplyOptions{
		FieldManager: "imagetest",
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed to apply cluster role binding: %w", err)
	}

	return nil
}

// monitor will block until the pod completes according to the entrypoint exit criteria.
func monitor(ctx context.Context, cli kubernetes.Interface, pod *corev1.Pod) error {
	ctx = clog.WithValues(ctx,
		"pod_name", pod.Name,
		"pod_namespace", pod.Namespace,
	)

	pw, err := cli.CoreV1().Pods(pod.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", pod.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch pod: %w", err)
	}
	defer pw.Stop()

	ew, err := cli.CoreV1().Events(pod.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch events: %w", err)
	}
	defer ew.Stop()

	logStarted := false
	logch := make(<-chan error, 1)

	for {
		select {
		case w, ok := <-pw.ResultChan():
			if !ok {
				continue
			}

			if w.Type == watch.Deleted {
				return fmt.Errorf("pod was deleted before tests could run")
			}

			p, ok := w.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			if !logStarted && p.Status.Phase == corev1.PodRunning {
				logStarted = true
				clog.InfoContext(ctx, "starting log stream")
				logch = startLogStream(ctx, cli, pod)
			}

			if w.Type == watch.Deleted {
				return fmt.Errorf("pod was deleted before tests could run")
			}

			for _, cs := range p.Status.ContainerStatuses {
				if cs.Name == SandboxContainerName && cs.State.Terminated != nil {
					clog.InfoContext(ctx, "sandbox container terminated",
						"exit_code", cs.State.Terminated.ExitCode,
						"reason", cs.State.Terminated.Reason,
						"message", cs.State.Terminated.Message,
					)

					switch ec := cs.State.Terminated.ExitCode; ec {
					case 0:
						clog.InfoContextf(ctx, "sandbox container completed successfully with exit code %d", ec)
						return nil
					case entrypoint.ProcessPausedCode:
						clog.InfoContextf(ctx, "sandbox container is paused with exit code %d", ec)
						return nil
					default:
						clog.ErrorContextf(ctx, "sandbox container failed with non-zero exit code %d", ec)
						return PodMonitorError{
							Name:      pod.Name,
							Namespace: pod.Namespace,
							Reason:    fmt.Sprintf("container %s terminated: %s", SandboxContainerName, cs.State.Terminated.Reason),
							ExitCode:  int(ec),
							Logs:      maybeLog(ctx, cli, pod),
						}
					}
				}
			}

		case w, ok := <-ew.ResultChan():
			if !ok {
				continue
			}

			e, ok := w.Object.(*corev1.Event)
			if !ok {
				continue
			}

			clog.InfoContext(ctx, "pod event",
				"message", e.Message,
				"reason", e.Reason,
				"name", e.Name,
			)

			// certain "events" can be a "termination", specifically during a PAUSE event, where the sandbox container has completed but the entrypoint is paused (the container is Running)
			if e.Reason == string(corev1.ResourceHealthStatusUnhealthy) && strings.Contains(e.Message, "Readiness probe failed") {
				parts := strings.SplitN(e.Message, ": ", 2)
				if len(parts) != 2 {
					// This is a non-termination event, ignore it and fallthrough
					continue
				}

				var msg struct {
					ExitCode int64  `json:"exit_code"`
					Msg      string `json:"msg"`
				}

				if err := json.Unmarshal([]byte(parts[1]), &msg); err != nil {
					// This is a non-termination event, ignore it and fallthrough
					continue
				}

				// At this point, this is a termination event, so figure out what to do

				ctx = clog.WithValues(ctx,
					"exit_code", msg.ExitCode,
					"probe message", msg.Msg,
				)

				if msg.ExitCode == entrypoint.ProcessPausedCode {
					clog.InfoContext(ctx, "test sandbox successfully completed and is paused")
					return nil
				} else {
					return PodMonitorError{
						Name:      pod.Name,
						Namespace: pod.Namespace,
						Reason:    e.Reason,
						ExitCode:  int(msg.ExitCode),
						Logs:      maybeLog(ctx, cli, pod),
					}
				}
			}

		case err := <-logch:
			if err != nil {
				return err
			}

		case <-ctx.Done():
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		}
	}
}

func startLogStream(ctx context.Context, cli kubernetes.Interface, pod *corev1.Pod) <-chan error {
	errch := make(chan error, 1)
	lreq := cli.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Follow:    true,
		Container: SandboxContainerName,
	})

	logs, err := lreq.Stream(ctx)
	if err != nil {
		errch <- fmt.Errorf("failed to initiate pod log stream: %w", err)
		close(errch)
		return errch
	}

	go func() {
		defer logs.Close()
		defer close(errch)

		scanner := bufio.NewScanner(logs)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				errch <- ctx.Err()
				return
			default:
				line := scanner.Text()
				clog.InfoContext(ctx, "received pod log line", drivers.LogAttributeKey, line)
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			clog.WarnContext(ctx, "error scanning logs, continuing", "error", err)
		}
	}()

	return errch
}

func maybeLog(ctx context.Context, cli kubernetes.Interface, pod *corev1.Pod) string {
	req := cli.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: SandboxContainerName,
		// limit to 1mb of logs
		LimitBytes: ptr.To(int64(1024 * 1024)),
	})

	rc, err := req.Stream(ctx)
	if err != nil {
		return fmt.Sprintf("failed to get logs: %v", err)
	}
	defer rc.Close()

	var buf bytes.Buffer
	if _, err = io.Copy(&buf, rc); err != nil {
		return fmt.Sprintf("failed to copy logs: %v", err)
	}

	return buf.String()
}

func (o *opts) pod() *corev1.Pod {
	wref := entrypoint.ImageRef
	if override := os.Getenv("IMAGETEST_ENTRYPOINT_REF"); override != "" {
		wref = override
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", o.Name),
			Namespace:    o.Namespace,
			Labels:       map[string]string{},
			Annotations:  map[string]string{},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:           o.Name,
			AutomountServiceAccountToken: ptr.To(false),
			SecurityContext:              &corev1.PodSecurityContext{},
			RestartPolicy:                corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{
					Name: "kube-api-access",
					VolumeSource: corev1.VolumeSource{
						Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{
								{
									ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
										Path:              "token",
										ExpirationSeconds: &[]int64{3600}[0],
									},
								},
								{
									ConfigMap: &corev1.ConfigMapProjection{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "kube-root-ca.crt",
										},
										Items: []corev1.KeyToPath{
											{
												Key:  "ca.crt",
												Path: "ca.crt",
											},
										},
									},
								},
							},
						},
					},
				},
				{
					Name: ArtifactContainerName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			Containers: []corev1.Container{
				// The primary test workspace
				{
					Name:  SandboxContainerName,
					Image: o.ImageRef.String(),
					SecurityContext: &corev1.SecurityContext{
						Privileged: &[]bool{true}[0],
						RunAsUser:  &[]int64{0}[0],
						RunAsGroup: &[]int64{0}[0],
					},
					Env: []corev1.EnvVar{
						{
							Name: "POD_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.name",
								},
							},
						},
						{
							Name: "POD_NAMESPACE",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.namespace",
								},
							},
						},
					},
					WorkingDir: o.WorkingDir,
					StartupProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: entrypoint.DefaultHealthCheckCommand,
							},
						},
						InitialDelaySeconds: 0,
						PeriodSeconds:       1,
						FailureThreshold:    60, // Allow the pod ample time to start
						TimeoutSeconds:      1,
						SuccessThreshold:    1,
					},
					// Once running, any failure should be captured by probe and considered a stop
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: entrypoint.DefaultHealthCheckCommand,
							},
						},
						InitialDelaySeconds: 0,
						PeriodSeconds:       1,
						FailureThreshold:    1,
						TimeoutSeconds:      1,
						SuccessThreshold:    1,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "kube-api-access",
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							ReadOnly:  true,
						},
						{
							Name:      ArtifactContainerName,
							MountPath: entrypoint.ArtifactsMountPath,
							ReadOnly:  false,
						},
					},
				},
				// The "sidecar" container used for storing artifacts to exfiltrate
				{
					Name:    ArtifactContainerName,
					Image:   wref,
					Command: []string{entrypoint.BinaryPath},
					Args:    []string{"wait"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      ArtifactContainerName,
							MountPath: entrypoint.ArtifactsMountPath,
							ReadOnly:  false,
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("16Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("50m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
					},
				},
			},
		},
	}

	for k, v := range o.ExtraLabels {
		pod.Labels[k] = v
	}

	for k, v := range o.ExtraAnnotations {
		pod.Annotations[k] = v
	}

	for k, v := range o.ExtraEnvs {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
			Name:  k,
			Value: v,
		})
	}

	return pod
}

func (o *opts) getArtifact(ctx context.Context, pod *corev1.Pod, result *drivers.RunResult) error {
	req := o.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.GetName()).
		Namespace(pod.GetNamespace()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: ArtifactContainerName,
			Command:   []string{entrypoint.BinaryPath, "export"},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(o.cfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	reader, writer := io.Pipe()

	var (
		stderrBuf  bytes.Buffer
		streamDone = make(chan error, 1)
	)

	go func() {
		defer writer.Close()
		clog.InfoContext(ctx, "starting stream to copy artifact")

		// Stream data from pod to the pipe writer
		streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: writer,
			Stderr: &stderrBuf,
		})

		stderr := stderrBuf.String()
		if stderr != "" {
			clog.WarnContextf(ctx, "received stderr while streaming from pod: %v", stderr)
		}

		if streamErr != nil {
			err := fmt.Errorf("stream error: %w (stderr: %q)", streamErr, stderr)
			writer.CloseWithError(err)
			clog.WarnContextf(ctx, "stream ended with error: %v", err)
			streamDone <- err
			return
		}

		clog.InfoContext(ctx, "stream finished successfully")
		streamDone <- nil
	}()

	artifact, err := drivers.NewRunArtifactResult(ctx, reader)
	if err != nil {
		return fmt.Errorf("failed to process artifact: %w", err)
	}

	select {
	case err := <-streamDone:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return fmt.Errorf("context cancelled: %w", ctx.Err())
	}

	result.Artifact = artifact
	return nil
}

type PodMonitorError struct {
	Name      string
	Namespace string
	Reason    string
	ExitCode  int
	Logs      string
	e         error
}

func (e PodMonitorError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "pod %s/%s error: %s", e.Namespace, e.Name, e.Reason)

	if e.ExitCode != -1 {
		fmt.Fprintf(&sb, ", exit_code=%d", e.ExitCode)
	}

	if e.e != nil {
		fmt.Fprintf(&sb, ", caused by: %v", e.e)
	}

	if e.Logs != "" {
		sb.WriteString(", Pod Logs:\n\n")
		sb.WriteString(e.Logs)
	}

	return sb.String()
}

func (e PodMonitorError) Unwrap() error {
	return e.e
}
