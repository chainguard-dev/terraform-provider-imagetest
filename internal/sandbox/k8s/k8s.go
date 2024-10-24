package k8s

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sandbox"
	"github.com/google/go-containerregistry/pkg/name"
	authorizationv1 "k8s.io/api/authorization/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

var _ sandbox.Sandbox = &k8s{}

type Request struct {
	sandbox.Request
}

// k8s is a sandbox that runs steps in a pod in a k8s cluster.
type k8s struct {
	request *Request
	cfg     *rest.Config
	cli     kubernetes.Interface
	pod     *corev1.Pod
	stack   *harness.Stack

	// gracePeriod is the grace period to use when deleting resources
	gracePeriod int64
}

func NewFromConfig(config *rest.Config, opts ...Option) (*k8s, error) {
	cli, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	k := &k8s{
		request: &Request{
			Request: sandbox.Request{
				Ref:       name.MustParseReference("cgr.dev/chainguard/kubectl:latest-dev"),
				Namespace: "default",
				Env:       make(map[string]string),
				Labels:    make(map[string]string),
			},
		},

		cfg:   config,
		cli:   cli,
		stack: harness.NewStack(),
	}

	for _, opt := range opts {
		if err := opt(k); err != nil {
			return nil, err
		}
	}

	return k, nil
}

func NewFromKubeconfig(kubeconfig []byte, opts ...Option) (*k8s, error) {
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	return NewFromConfig(config, opts...)
}

func (k *k8s) Run(ctx context.Context) error {
	sa, _, err := k.preflight(ctx)
	if err != nil {
		return fmt.Errorf("preflight checks failed: %w", err)
	}

	template := k.createPodTemplateSpec(sa.Name)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.request.Name,
			Namespace: sa.Namespace,
			Labels:    k.request.Labels,
		},
		Spec: batchv1.JobSpec{
			Template:     *template,
			BackoffLimit: new(int32), // Don't retry on failure
		},
	}

	job.Spec.Template.Spec.Containers[0].Command = []string{}

	obj, err := k.cli.BatchV1().Jobs(sa.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating job: %w", err)
	}

	if err := k.stack.Add(func(ctx context.Context) error {
		return k.cli.BatchV1().Jobs(obj.Namespace).Delete(ctx, obj.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &k.gracePeriod,
		})
	}); err != nil {
		return fmt.Errorf("adding job teardown to stack: %w", err)
	}

	watcher, err := k.cli.BatchV1().Jobs(obj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", obj.Name),
	})
	if err != nil {
		return fmt.Errorf("watching job: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Modified:
			job, ok := event.Object.(*batchv1.Job)
			if !ok {
				return fmt.Errorf("unexpected object type in watch event")
			}
			if job.Status.Succeeded > 0 {
				return nil
			}
			if job.Status.Failed > 0 {
				return fmt.Errorf("job failed")
			}
		case watch.Error:
			return fmt.Errorf("watch error: %v", event.Object)
		}
	}

	return fmt.Errorf("job watch ended without completion")
}

// Start implements sandbox.Sandbox.
func (k *k8s) Start(ctx context.Context) (sandbox.Runner, error) {
	sa, _, err := k.preflight(ctx)
	if err != nil {
		return nil, fmt.Errorf("preflight checks failed: %w", err)
	}

	template := k.createPodTemplateSpec(sa.Name)
	pod := &corev1.Pod{
		ObjectMeta: template.ObjectMeta,
		Spec:       template.Spec,
	}

	// Now create the stupidly privileged pod that we'll use to run the steps
	obj, err := k.cli.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating pod: %w", err)
	}
	k.pod = pod

	if err := k.stack.Add(func(ctx context.Context) error {
		return k.cli.CoreV1().Pods(obj.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &k.gracePeriod,
		})
	}); err != nil {
		return nil, fmt.Errorf("adding pod teardown to stack: %w", err)
	}

	if err := waitForPod(ctx, k.cli, pod); err != nil {
		return nil, fmt.Errorf("waiting for pod to be running: %w", err)
	}

	return &response{
		cmd: func(ctx context.Context, cmd harness.Command) error {
			req := k.cli.CoreV1().RESTClient().Post().Resource("pods").
				Name(pod.Name).
				Namespace(pod.Namespace).
				SubResource("exec").
				VersionedParams(&corev1.PodExecOptions{
					Container: pod.Spec.Containers[0].Name,
					Command:   []string{"sh", "-c", cmd.Args},
					Stdout:    true,
					Stderr:    true,
				}, scheme.ParameterCodec)

			spdyexec, err := remotecommand.NewSPDYExecutor(k.cfg, "POST", req.URL())
			if err != nil {
				return fmt.Errorf("creating exec request: %w", err)
			}
			wsexec, err := remotecommand.NewWebSocketExecutor(k.cfg, "GET", req.URL().String())
			if err != nil {
				return fmt.Errorf("creating exec request: %w", err)
			}

			exec, err := remotecommand.NewFallbackExecutor(wsexec, spdyexec, httpstream.IsUpgradeFailure)
			if err != nil {
				return fmt.Errorf("creating exec request: %w", err)
			}

			return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
				Stdout: cmd.Stdout,
				Stderr: cmd.Stderr,
			})
		},
	}, nil
}

// Destroy implements sandbox.Sandbox.
func (k *k8s) Destroy(ctx context.Context) error {
	return k.stack.Teardown(ctx)
}

type response struct {
	cmd func(context.Context, harness.Command) error
}

// Run implements sandbox.Runner.
func (r *response) Run(ctx context.Context, cmd harness.Command) error {
	return r.cmd(ctx, cmd)
}

func (k *k8s) createPodTemplateSpec(saName string) *corev1.PodTemplateSpec {
	template := &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.request.Name,
			Namespace: k.request.Namespace,
			Labels:    k.request.Labels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: saName,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:  &k.request.User,
				RunAsGroup: &k.request.Group,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "sandbox",
					Image: k.request.Ref.String(),
					Env: []corev1.EnvVar{
						{
							Name:  "IMAGETEST",
							Value: "true",
						},
					},
					WorkingDir: k.request.WorkingDir,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "kube-api-access",
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							ReadOnly:  true,
						},
					},
				},
			},
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
		},
	}

	envs := make([]corev1.EnvVar, 0, len(k.request.Env))
	for key, val := range k.request.Env {
		envs = append(envs, corev1.EnvVar{
			Name:  key,
			Value: val,
		})
	}
	template.Spec.Containers[0].Env = append(template.Spec.Containers[0].Env, envs...)

	if k.request.Entrypoint != nil {
		template.Spec.Containers[0].Command = k.request.Entrypoint
	}

	if k.request.Cmd != nil {
		template.Spec.Containers[0].Args = k.request.Cmd
	}

	if k.request.Resources.Limits != nil {
		template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{}
	}

	if k.request.Resources.Requests != nil {
		template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{}
	}

	return template
}

func (k *k8s) preflight(ctx context.Context) (*corev1.ServiceAccount, *rbacv1.ClusterRoleBinding, error) {
	resp, err := k.cli.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: k.request.Namespace,
				Verb:      "create",
				Group:     "apps",
				Resource:  "pods",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("validating user permissions: %w", err)
	}

	if !resp.Status.Allowed {
		return nil, nil, fmt.Errorf("user does not have permission to create pods")
	}

	// Create the namespace only if it doesn't already exist
	ns, err := k.cli.CoreV1().Namespaces().Get(ctx, k.request.Namespace, metav1.GetOptions{})
	if err != nil {
		// if the namespace doesn't exist, create it
		if errors.IsNotFound(err) {
			ns, err = k.cli.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: k.request.Namespace,
					Labels: map[string]string{
						"dev.chainguard.imagetest": "true",
					},
				},
			}, metav1.CreateOptions{})
			if err != nil {
				return nil, nil, fmt.Errorf("creating imagetest namespace: %w", err)
			}

			if err := k.stack.Add(func(ctx context.Context) error {
				return k.cli.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{
					GracePeriodSeconds: &k.gracePeriod,
				})
			}); err != nil {
				return nil, nil, fmt.Errorf("adding namespace teardown to stack: %w", err)
			}
		} else {
			return nil, nil, fmt.Errorf("getting imagetest namespace: %w", err)
		}
	}

	if k.request.Name == "" {
		dryns, err := k.cli.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "imagetest-",
			},
		}, metav1.CreateOptions{
			DryRun: []string{"All"},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("creating dryrun namespace: %w", err)
		}
		k.request.Name = dryns.Name
	}

	// Create the service account
	sa, err := k.cli.CoreV1().ServiceAccounts(ns.Name).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.request.Name,
			Namespace: ns.Name,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("creating service account: %w", err)
	}

	if err := k.stack.Add(func(ctx context.Context) error {
		return k.cli.CoreV1().ServiceAccounts(ns.Name).Delete(ctx, sa.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &k.gracePeriod,
		})
	}); err != nil {
		return nil, nil, fmt.Errorf("adding service account teardown to stack: %w", err)
	}

	// Create the role binding
	rb, err := k.cli.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.request.Name,
			Namespace: ns.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("creating role binding: %w", err)
	}

	if err := k.stack.Add(func(ctx context.Context) error {
		return k.cli.RbacV1().ClusterRoleBindings().Delete(ctx, rb.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &k.gracePeriod,
		})
	}); err != nil {
		return nil, nil, fmt.Errorf("adding role binding teardown to stack: %w", err)
	}

	return sa, rb, nil
}

func waitForPod(ctx context.Context, cli kubernetes.Interface, pod *corev1.Pod) error {
	// Block until the pod is running
	watcher, err := cli.CoreV1().Pods(pod.Namespace).Watch(ctx, metav1.ListOptions{
		Watch:         true,
		FieldSelector: "metadata.name=" + pod.Name,
	})
	if err != nil {
		return fmt.Errorf("creating pod: %w", err)
	}
	defer watcher.Stop()

	ch := watcher.ResultChan()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-ch:
			if !ok {
				return fmt.Errorf("channel closed")
			}
			switch event.Type {
			case watch.Added, watch.Modified:
				pod, ok := event.Object.(*corev1.Pod)
				if !ok {
					return fmt.Errorf("failed to cast event object to pod")
				}
				if pod.Status.Phase == corev1.PodRunning {
					return nil
				}
			case watch.Deleted:
				return fmt.Errorf("pod was deleted")
			case watch.Error:
				return fmt.Errorf("watch error: %v", event.Object)
			}
		}
	}
}
