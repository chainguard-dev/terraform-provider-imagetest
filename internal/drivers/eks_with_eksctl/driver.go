package ekswitheksctl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
)

type driver struct {
	name string

	region      string
	clusterName string
	namespace   string
	kubeconfig  string
	kcli        kubernetes.Interface
}

type DriverOpts func(*driver) error

func NewDriver(n string, opts ...DriverOpts) (drivers.Tester, error) {
	k := &driver{
		name:      n,
		region:    "us-west-2",
		namespace: "imagetest",
	}

	if _, err := exec.LookPath("eksctl"); err != nil {
		return nil, fmt.Errorf("eksctl not found in $PATH: %w", err)
	}

	for _, opt := range opts {
		if err := opt(k); err != nil {
			return nil, err
		}
	}

	return k, nil
}

func (k *driver) eksctl(ctx context.Context, args ...string) error {
	args = append(args, []string{
		"--color", "false", // Disable color output
		"--region", k.region,
	}...)
	clog.FromContext(ctx).Infof("eksctl %v", args)
	cmd := exec.CommandContext(ctx, "eksctl", args...)
	cmd.Env = os.Environ() // Copy the environment
	cmd.Env = append(cmd.Env, "KUBECONFIG="+k.kubeconfig)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("eksctl %v: %v: %s", args, err, out)
	}
	return nil
}

func (k *driver) Setup(ctx context.Context) error {
	log := clog.FromContext(ctx)

	if n, ok := os.LookupEnv("IMAGETEST_EKS_CLUSTER"); ok {
		log.Infof("Using cluster name from IMAGETEST_EKS_CLUSTER: %s", n)
		k.clusterName = n
	} else {
		uid := "imagetest-" + uuid.New().String()
		log.Infof("Using random cluster name: %s", uid)
		k.clusterName = uid
	}

	cfg, err := os.Create(filepath.Join(os.TempDir(), k.clusterName))
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	log.Infof("Using kubeconfig: %s", cfg.Name())
	k.kubeconfig = cfg.Name()

	if _, ok := os.LookupEnv("IMAGETEST_EKS_CLUSTER"); ok {
		if err := k.eksctl(ctx, "utils", "write-kubeconfig", "--cluster", k.clusterName, "--kubeconfig", k.kubeconfig); err != nil {
			return fmt.Errorf("eksctl utils write-kubeconfig: %w", err)
		}
	} else {
		if err := k.eksctl(ctx, "create", "cluster",
			"--node-private-networking=false",
			"--vpc-nat-mode=Disable",
			"--kubeconfig="+k.kubeconfig,
			"--name="+k.clusterName,
		); err != nil {
			return fmt.Errorf("eksctl create cluster: %w", err)
		}
		log.Infof("Created cluster %s", k.clusterName)
	}

	config, err := clientcmd.BuildConfigFromFlags("", k.kubeconfig)
	if err != nil {
		return fmt.Errorf("building kubeconfig: %w", err)
	}

	kcli, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}
	k.kcli = kcli

	return k.preflight(ctx)
}

func (k *driver) Teardown(ctx context.Context) error {
	if v := os.Getenv("IMAGETEST_EKS_SKIP_TEARDOWN"); v == "true" {
		clog.FromContext(ctx).Info("Skipping EKS teardown due to IMAGETEST_EKS_SKIP_TEARDOWN=true")
		return nil
	}
	if err := k.eksctl(ctx, "delete", "cluster", "--name", k.clusterName); err != nil {
		return fmt.Errorf("eksctl delete cluster: %w", err)
	}
	return nil
}

func (k *driver) Run(ctx context.Context, ref name.Reference) error {
	// TODO: share this with k3sindocker driver
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "imagetest-",
			Namespace:    k.namespace,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "imagetest",
			SecurityContext:    &corev1.PodSecurityContext{},
			RestartPolicy:      corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{{
				Name: "kube-api-access",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Path:              "token",
								ExpirationSeconds: &[]int64{3600}[0],
							},
						}, {
							ConfigMap: &corev1.ConfigMapProjection{
								LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"},
								Items:                []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
							},
						}, {
							DownwardAPI: &corev1.DownwardAPIProjection{
								Items: []corev1.DownwardAPIVolumeFile{{
									Path: "namespace",
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								}},
							},
						}},
					},
				},
			}},
			Containers: []corev1.Container{{
				// The primary test workspace
				Name:  "sandbox",
				Image: ref.String(),
				SecurityContext: &corev1.SecurityContext{
					Privileged: &[]bool{true}[0],
					RunAsUser:  &[]int64{0}[0],
					RunAsGroup: &[]int64{0}[0],
				},
				Env: []corev1.EnvVar{
					{Name: "IMAGETEST", Value: "true"},
					{Name: "IMAGETEST_DRIVER", Value: "eks_with_eksctl"},
					{
						Name: "POD_NAME",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								FieldPath: "metadata.name",
							},
						},
					}, {
						Name: "POD_NAMESPACE",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								FieldPath: "metadata.namespace",
							},
						},
					},
				},
				WorkingDir:             "/imagetest",
				TerminationMessagePath: entrypoint.DefaultStderrLogPath,
				StartupProbe: &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: entrypoint.DefaultHealthCheckCommand}},
					InitialDelaySeconds: 0,
					PeriodSeconds:       1,
					FailureThreshold:    60, // Allow the pod ample time to start
					TimeoutSeconds:      1,
					SuccessThreshold:    1,
				},
				// Once running, any failure should be captured by probe and considered a stop
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: entrypoint.DefaultHealthCheckCommand}},
					InitialDelaySeconds: 0,
					PeriodSeconds:       1,
					FailureThreshold:    1,
					TimeoutSeconds:      1,
					SuccessThreshold:    1,
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "kube-api-access",
					MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
					ReadOnly:  true,
				}},
			}},
		},
	}

	pobj, err := k.kcli.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create pod: %w", err)
	}

	plog := clog.FromContext(ctx).With("pod_name", pobj.Name, "pod_namespace", pobj.Namespace, "tests_resource", k.name)

	ew, err := k.kcli.CoreV1().Events(pobj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", pobj.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch events: %w", err)
	}
	defer ew.Stop()

	pw, err := k.kcli.CoreV1().Pods(pobj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", pobj.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch pod: %w", err)
	}
	defer pw.Stop()

	logsErrCh := make(chan error, 1)
	logStreamOnce := sync.Once{}

	logStream := func() {
		lreq := k.kcli.CoreV1().Pods(k.namespace).GetLogs(pobj.Name, &corev1.PodLogOptions{Follow: true, Container: "sandbox"})
		logs, err := lreq.Stream(ctx)
		if err != nil {
			logsErrCh <- fmt.Errorf("failed to initiate pod log stream: %w", err)
			return
		}

		go func() {
			defer logs.Close()

			scanner := bufio.NewScanner(logs)
			for scanner.Scan() {
				select {
				case <-ctx.Done():
					close(logsErrCh)
					return
				default:
					line := scanner.Text()
					plog.With("message", line).Info("received pod log line")
				}
			}

			if err := scanner.Err(); err != nil && err != io.EOF {
				logsErrCh <- fmt.Errorf("scanning logs: %w", err)
			}

			close(logsErrCh)
		}()
	}

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

			plog.With("message", e.Message, "reason", e.Reason, "name", e.Name).Info("received event")

			if e.Reason == string(corev1.ResourceHealthStatusUnhealthy) && started && strings.Contains(e.Message, "Readiness probe failed") {
				// this filters out "Readiness probe errored" events, which are always
				// fired after a pod successfully completes (0/1 Completed)
				plog.Info("test sandbox pod failed and is paused in debug mode")
				return fmt.Errorf("test sandbox failed in debug mode and is now paused\n\n%s", e.Message)
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
				logStreamOnce.Do(logStream)

				for _, cs := range p.Status.ContainerStatuses {
					if cs.Name == "sandbox" && cs.State.Running != nil && *cs.Started {
						plog.Info("test sandbox pod has started")
						started = true
						break
					}
				}

			case corev1.PodSucceeded:
				plog.Info("test sandbox pod completed successfully")
				return nil

			case corev1.PodFailed, corev1.PodUnknown:
				plog.Info("test sandbox pod exited with failure")

				err := fmt.Errorf("pod %s/%s exited with failure", pobj.Name, pobj.Namespace)
				for _, cs := range p.Status.ContainerStatuses {
					if cs.Name == "sandbox" {
						if cs.State.Terminated != nil {
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

		case <-ctx.Done():
		case err, ok := <-logsErrCh:
			if !ok {
				continue
			}
			if err != nil {
				return fmt.Errorf("failed to stream logs: %w", err)
			}
		}
	}
}

// preflight creates the necessary k8s resources to run the tests in pods.
func (k *driver) preflight(ctx context.Context) error {
	// Check that we can actually do things with the client
	resp, err := k.kcli.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: k.namespace,
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
		return fmt.Errorf("user does not have permission to create pods")
	}

	// Create the namespace
	if _, err := k.kcli.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: k.namespace,
		},
	}, metav1.CreateOptions{}); k8serrors.IsAlreadyExists(err) {
		log.Infof("Namespace %q already exists", k.namespace)
	} else if err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// Create the relevant rbac
	if _, err := k.kcli.CoreV1().ServiceAccounts(k.namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.namespace,
			Namespace: k.namespace,
		},
	}, metav1.CreateOptions{}); k8serrors.IsAlreadyExists(err) {
		log.Infof("ServiceAccount %q already exists", k.namespace)
	} else if err != nil {
		return fmt.Errorf("failed to create service account: %w", err)
	}

	// Create the role binding
	if _, err := k.kcli.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.namespace,
			Namespace: k.namespace,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      k.namespace,
			Namespace: k.namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
	}, metav1.CreateOptions{}); k8serrors.IsAlreadyExists(err) {
		log.Infof("ClusterRoleBinding %s already exists", "imagetest")
	} else if err != nil {
		return fmt.Errorf("failed to create role binding: %w", err)
	}

	return nil
}
