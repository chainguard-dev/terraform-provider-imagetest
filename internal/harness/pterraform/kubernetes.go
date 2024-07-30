package pterraform

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
	authorizationv1 "k8s.io/api/authorization/v1"
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

var _ Runner = &k8s{}

type KubernetesConnection struct {
	Kubeconfig     string `json:"kubeconfig"`
	KubeconfigPath string `json:"kubeconfig_path"`
	SandboxImage   string `json:"sandbox_image"`
}

func (k KubernetesConnection) config() (*rest.Config, error) {
	if k.KubeconfigPath != "" && k.Kubeconfig != "" {
		return nil, fmt.Errorf("only one of kubeconfig or kubeconfig_path can be set")
	}

	if k.KubeconfigPath != "" {
		config, err := clientcmd.BuildConfigFromFlags("", k.KubeconfigPath)
		if err != nil {
			return nil, err
		}
		return config, nil
	}

	if k.Kubeconfig != "" {
		config, err := clientcmd.RESTConfigFromKubeConfig([]byte(k.Kubeconfig))
		if err != nil {
			return nil, err
		}
		return config, nil
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
}

type k8s struct {
	SandboxImage name.Reference

	cfg  *rest.Config
	cli  kubernetes.Interface
	pod  *corev1.Pod
	once sync.Once
}

// newK8sRunner sets up a new k8s connector that creates a pod for steps to be run
// in. It returns after the pod is created and running.
func newK8sRunner(_ context.Context, conn *KubernetesConnection) (*k8s, error) {
	cfg, err := conn.config()
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	if conn.SandboxImage == "" {
		conn.SandboxImage = "cgr.dev/chainguard/kubectl:latest-dev"
	}

	ref, err := name.ParseReference(conn.SandboxImage)
	if err != nil {
		return nil, fmt.Errorf("parsing sandbox image reference: %w", err)
	}

	return &k8s{
		SandboxImage: ref,
		cli:          cli,
		cfg:          cfg,
		once:         sync.Once{},
	}, nil
}

// Run implements Connector.
func (k *k8s) Run(ctx context.Context, cmd harness.Command) error {
	var initErr error
	k.once.Do(func() {
		initErr = k.init(ctx)
	})

	if initErr != nil {
		return initErr
	}

	// create an exec request and an executor for running commands in the pod
	req := k.cli.CoreV1().RESTClient().Post().Resource("pods").
		Name(k.pod.Name).
		Namespace(k.pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: k.pod.Spec.Containers[0].Name,
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

	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: cmd.Stdout,
		Stderr: cmd.Stderr,
	}); err != nil {
		return fmt.Errorf("running command: %w", err)
	}

	return nil
}

func (k *k8s) init(ctx context.Context) error {
	resp, err := k.cli.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: "imagetest",
				Verb:      "create",
				Group:     "apps",
				Resource:  "pods",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("validating user permissions: %w", err)
	}

	if !resp.Status.Allowed {
		return fmt.Errorf("user does not have permission to create pods")
	}

	// get the imagetest namespace
	ns, err := k.cli.CoreV1().Namespaces().Get(ctx, "imagetest", metav1.GetOptions{})
	if err != nil {
		// if the namespace doesn't exist, create it
		if errors.IsNotFound(err) {
			ns, err = k.cli.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "imagetest",
					Labels: map[string]string{
						"dev.chainguard.imagetest": "true",
					},
				},
			}, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("creating imagetest namespace: %w", err)
			}
		} else {
			return fmt.Errorf("getting imagetest namespace: %w", err)
		}
	}

	// create the laundry list of RBAC related resources
	cr, err := k.cli.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "imagetest-superuser-",
			Labels: map[string]string{
				"dev.chainguard.imagetest": "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"*"},
				APIGroups: []string{""},
				Resources: []string{"*"},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating cluster role: %w", err)
	}

	// create the serviceaccount
	sa, err := k.cli.CoreV1().ServiceAccounts(ns.Name).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "imagetest-",
			Namespace:    ns.Name,
			Labels: map[string]string{
				"dev.chainguard.imagetest": "true",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating service account: %w", err)
	}

	// finally, create the role binding
	_, err = k.cli.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "imagetest-superuser-",
			Labels: map[string]string{
				"dev.chainguard.imagetest": "true",
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      sa.Name,
				Namespace: ns.Name,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     cr.Name,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating cluster role binding: %w", err)
	}

	pod, err := k.cli.CoreV1().Pods("imagetest").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "imagetest-",
			Namespace:    ns.Name,
			Labels: map[string]string{
				"dev.chainguard.imagetest": "true",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: sa.Name,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:  &[]int64{0}[0],
				RunAsGroup: &[]int64{0}[0],
			},
			Containers: []corev1.Container{
				{
					Name:  "sandbox",
					Image: k.SandboxImage.String(),
					Command: []string{
						"tail",
						"-f",
						"/dev/null",
					},
					Env: []corev1.EnvVar{
						{
							Name:  "IMAGETEST",
							Value: "true",
						},
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
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating pod: %w", err)
	}
	k.pod = pod

	// Wait for the pod to be ready
	watcher, err := k.cli.CoreV1().Pods("imagetest").Watch(ctx, metav1.ListOptions{
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
