package pod

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/google/go-containerregistry/pkg/name"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1apply "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
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
}

type RunOpts func(*opts) error

func Run(ctx context.Context, client kubernetes.Interface, options ...RunOpts) error {
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

		client: client,
	}

	for _, opt := range options {
		if err := opt(&o); err != nil {
			return err
		}
	}

	if err := o.preflight(ctx); err != nil {
		return err
	}

	pobj, err := o.client.CoreV1().Pods(o.Namespace).
		Create(ctx, o.pod(), metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create pod: %w", err)
	}

	plog := clog.FromContext(ctx).With("pod_name", pobj.Name, "pod_namespace", pobj.Namespace)

	ew, err := o.client.CoreV1().Events(pobj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", pobj.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch events: %w", err)
	}
	defer ew.Stop()

	pw, err := o.client.CoreV1().Pods(pobj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", pobj.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch pod: %w", err)
	}
	defer pw.Stop()

	var logErrCh <-chan error
	logStreamOnce := sync.Once{}

	started := false
	for {
		select {
		case w, ok := <-ew.ResultChan():
			if !ok {
				continue
			}

			e, ok := w.Object.(*corev1.Event)
			if !ok {
				continue
			}

			plog.InfoContext(ctx, "noticed event", "message", e.Message, "reason", e.Reason, "name", e.Name)

			if e.Reason == string(corev1.ResourceHealthStatusUnhealthy) && started && strings.Contains(e.Message, "Readiness probe failed") {
				// this filters out "Readiness probe errored" events, which are always
				// fired after a pod successfully completes (0/1 Completed)
				plog.InfoContext(ctx, "test sandbox pod failed and is paused in debug mode")

				// nastiness here is to parse the health check's exit code from the
				// readiness probe events' message. there's got to be a better way...
				parts := strings.Split(e.Message, ": ")
				if len(parts) != 2 {
					// just return the whole message
					return fmt.Errorf("test sandbox failed in debug mode and is now paused\n\n%s", e.Message)
				}

				// extract the exit code from the error
				var rmsg struct {
					ExitCode int64  `json:"exit_code"`
					Msg      string `json:"msg"`
				}
				if err := json.Unmarshal([]byte(parts[1]), &rmsg); err != nil {
					clog.WarnContext(ctx, "failed to parse healthcheck message", "message", e.Message, "part", parts[1])
					return fmt.Errorf("test sandbox failed in debug mode and is now paused\n\n%s", e.Message)
				}

				if rmsg.ExitCode == entrypoint.ProcessPausedCode {
					clog.InfoContext(ctx, "test sandbox successfully completed and is paused", "exit_code", rmsg.ExitCode, "probe_message", rmsg.Msg)
					return nil
				}

				return fmt.Errorf("test sandbox failed in debug mode and is now paused (exit_code: %d)\n\n%s", rmsg.ExitCode, rmsg.Msg)
			}

		case w, ok := <-pw.ResultChan():
			if !ok {
				continue
			}

			p, ok := w.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			if w.Type == watch.Deleted {
				return fmt.Errorf("pod was deleted before tests could run")
			}

			switch p.Status.Phase {
			case corev1.PodRunning:
				logStreamOnce.Do(func() {
					logErrCh = o.startLogStream(ctx, pobj.Name)
				})

				for _, cs := range p.Status.ContainerStatuses {
					if cs.Name == "sandbox" && cs.State.Running != nil && *cs.Started {
						plog.InfoContext(ctx, "test sandbox pod has started")
						started = true
						break
					}
				}

			case corev1.PodSucceeded:
				plog.InfoContext(ctx, "test sandbox pod completed successfully")
				return nil

			case corev1.PodFailed, corev1.PodUnknown:
				plog.InfoContext(ctx, "test sandbox pod exited with failure")

				err := fmt.Errorf("pod %s/%s exited with failure", pobj.Name, pobj.Namespace)
				for _, cs := range p.Status.ContainerStatuses {
					if cs.Name == "sandbox" {
						if cs.State.Terminated != nil {
							if cs.State.Terminated.ExitCode == entrypoint.ProcessPausedCode {
								return nil
							}

							err = fmt.Errorf("%w\n\nexit code: %d, reason: %s, message: %s", err,
								cs.State.Terminated.ExitCode,
								cs.State.Terminated.Reason,
								cs.State.Terminated.Message,
							)
						}
					}
				}
				return err
			}

		case err, ok := <-logErrCh:
			if ok && err != nil {
				return fmt.Errorf("failed to stream logs: %w", err)
			}

		case <-ctx.Done():
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		}
	}
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

func (o *opts) startLogStream(ctx context.Context, podName string) <-chan error {
	errch := make(chan error, 1)
	lreq := o.client.CoreV1().Pods(o.Namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    true,
		Container: "sandbox",
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
			errch <- fmt.Errorf("scanning logs: %w", err)
		}
	}()

	return errch
}

func (o *opts) pod() *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", o.Name),
			Namespace:    o.Namespace,
			Labels:       map[string]string{},
			Annotations:  map[string]string{},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: o.Name,
			SecurityContext:    &corev1.PodSecurityContext{},
			RestartPolicy:      corev1.RestartPolicyNever,
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
								{
									DownwardAPI: &corev1.DownwardAPIProjection{
										Items: []corev1.DownwardAPIVolumeFile{
											{
												Path: "namespace",
												FieldRef: &corev1.ObjectFieldSelector{
													FieldPath: "metadata.namespace",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			Containers: []corev1.Container{
				// The primary test workspace
				{
					Name:  "sandbox",
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
					WorkingDir:             o.WorkingDir,
					TerminationMessagePath: entrypoint.DefaultStderrLogPath,
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
