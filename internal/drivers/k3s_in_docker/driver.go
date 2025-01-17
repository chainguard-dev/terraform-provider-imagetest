package k3sindocker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// driver is a k8s driver that spins up a k3s cluster in docker alongside a
// network attached sandbox.
type driver struct {
	ImageRef      name.Reference // The image reference to use for the k3s cluster
	CNI           bool           // Toggles whether the default k3s CNI is enabled
	Traefik       bool           // Toggles whether the default k3s traefik ingress controller is enabled
	MetricsServer bool           // Toggles whether the default k3s metrics server is enabled
	NetworkPolicy bool           // Toggles whether the default k3s network policy controller is enabled
	Snapshotter   string         // The containerd snapshotter to use
	Registries    map[string]*K3sRegistryConfig
	Namespace     string // The namespace to use for the test pods

	kubeconfigWritePath string // When set, the generated kubeconfig will be written to this path on the host

	name  string
	stack *harness.Stack
	kcli  kubernetes.Interface
}

type K3sRegistryConfig struct {
	Auth    *K3sRegistryAuthConfig
	Mirrors *K3sRegistryMirrorConfig
}

type K3sRegistryAuthConfig struct {
	Username string
	Password string
	Auth     string
}

type K3sRegistryMirrorConfig struct {
	Endpoints []string
}

func NewDriver(n string, opts ...DriverOpts) (drivers.Tester, error) {
	k := &driver{
		ImageRef:      name.MustParseReference("cgr.dev/chainguard/k3s:latest-dev"),
		CNI:           true,
		Traefik:       false,
		MetricsServer: false,
		NetworkPolicy: false,
		Namespace:     "imagetest",

		name:  n,
		stack: harness.NewStack(),
	}

	for _, opt := range opts {
		if err := opt(k); err != nil {
			return nil, err
		}
	}

	return k, nil
}

func (k *driver) Setup(ctx context.Context) error {
	cli, err := docker.New()
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}

	contents := []*docker.Content{}

	ktpl := fmt.Sprintf(`
tls-san: "%[1]s"
disable:
{{- if not .Traefik }}
  - traefik
{{- end }}
{{- if not .MetricsServer }}
  - metrics-server
{{- end }}
{{- if not .NetworkPolicy }}
  - network-policy
{{- end }}
{{- if not .CNI }}
flannel-backend: none
{{- end }}
snapshotter: "{{ .Snapshotter }}"
`, k.name)

	var tplo bytes.Buffer
	t := template.Must(template.New("k3s-config").Parse(ktpl))
	if err := t.Execute(&tplo, k); err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	rtpl := `
mirrors:
  {{- range $k, $v := .Registries }}
  {{- if $v.Mirrors }}
  "{{ $k }}":
    endpoint:
      {{- range $v.Mirrors.Endpoints }}
      - "{{ . }}"
      {{- end }}
  {{- end }}
  {{- end}}

configs:
  {{- range $k, $v := .Registries }}
  {{- if $v.Auth }}
  "{{ $k }}":
    auth:
      username: "{{ $v.Auth.Username }}"
      password: "{{ $v.Auth.Password }}"
      auth: "{{ $v.Auth.Auth }}"
  {{- end }}
  {{- end }}
`

	var rto bytes.Buffer
	t = template.Must(template.New("k3s-registries").Parse(rtpl))
	if err := t.Execute(&rto, k); err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	contents = append(contents,
		docker.NewContentFromString(tplo.String(), "/etc/rancher/k3s/config.yaml"),
		docker.NewContentFromString(rto.String(), "/etc/rancher/k3s/registries.yaml"),
	)

	nw, err := cli.CreateNetwork(ctx, &docker.NetworkRequest{})
	if err != nil {
		return fmt.Errorf("creating docker network: %w", err)
	}

	if err := k.stack.Add(func(ctx context.Context) error {
		return cli.RemoveNetwork(ctx, nw)
	}); err != nil {
		return fmt.Errorf("adding network teardown to stack: %w", err)
	}

	clog.InfoContext(ctx, "starting k3s in docker",
		"image_ref", k.ImageRef.String(),
		"network_id", nw.ID,
	)

	resp, err := cli.Start(ctx, &docker.Request{
		Name:       k.name,
		Ref:        k.ImageRef,
		Cmd:        []string{"server"},
		Privileged: true, // This doesn't work without privilege, so don't make it configurable
		Networks: []docker.NetworkAttachment{{
			ID:   nw.ID,
			Name: nw.Name,
		}},
		Labels: map[string]string{
			"dev.chainguard.imagetest/kubeconfig-path": k.kubeconfigWritePath,
		},
		Mounts: []mount.Mount{{
			Type:   mount.TypeTmpfs,
			Target: "/run",
		}, {
			Type:   mount.TypeTmpfs,
			Target: "/tmp",
		}},
		// Default requests only to the bare minimum k3s needs to run properly
		// https://docs.k3s.io/installation/requirements#hardware
		Resources: docker.ResourcesRequest{
			MemoryRequest: resource.MustParse("2Gi"),
			CpuRequest:    resource.MustParse("1"),
		},
		HealthCheck: &v1.HealthcheckConfig{
			Test:          []string{"CMD", "/bin/sh", "-c", "kubectl get --raw='/healthz'"},
			Interval:      2 * time.Second,
			Timeout:       5 * time.Second,
			Retries:       10,
			StartInterval: 1 * time.Second,
		},
		PortBindings: nat.PortMap{
			nat.Port(strconv.Itoa(6443)): []nat.PortBinding{{
				HostIP:   "127.0.0.1",
				HostPort: "", // Lets the docker daemon pick a random port
			}},
		},
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Contents:   contents,
	})
	if err != nil {
		return fmt.Errorf("starting k3s: %w", err)
	}

	if err := k.stack.Add(func(ctx context.Context) error {
		return cli.Remove(ctx, resp)
	}); err != nil {
		return err
	}

	kcfgraw, err := resp.ReadFile(ctx, "/etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return fmt.Errorf("getting kubeconfig: %w", err)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig(kcfgraw)
	if err != nil {
		return fmt.Errorf("creating kubernetes config: %w", err)
	}

	config.Host = fmt.Sprintf("https://127.0.0.1:%s", resp.NetworkSettings.Ports["6443/tcp"][0].HostPort)

	kcli, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}
	k.kcli = kcli

	if k.kubeconfigWritePath != "" {
		kcfg, err := clientcmd.Load(kcfgraw)
		if err != nil {
			return fmt.Errorf("loading kubeconfig: %w", err)
		}

		for _, cluster := range kcfg.Clusters {
			cluster.Server = config.Host
		}

		if err := os.MkdirAll(filepath.Dir(k.kubeconfigWritePath), 0755); err != nil {
			return fmt.Errorf("failed to create kubeconfig directory: %w", err)
		}

		clog.InfoContext(ctx, "writing kubeconfig to file", "path", k.kubeconfigWritePath)
		if err := clientcmd.WriteToFile(*kcfg, k.kubeconfigWritePath); err != nil {
			return fmt.Errorf("writing kubeconfig: %w", err)
		}
	}

	return k.preflight(ctx)
}

func (k *driver) Teardown(ctx context.Context) error {
	return k.stack.Teardown(ctx)
}

func (k *driver) Run(ctx context.Context, ref name.Reference) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "imagetest-",
			Namespace:    k.Namespace,
			Labels:       map[string]string{},
			Annotations:  map[string]string{},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "imagetest",
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
					Image: ref.String(),
					SecurityContext: &corev1.SecurityContext{
						Privileged: &[]bool{true}[0],
						RunAsUser:  &[]int64{0}[0],
						RunAsGroup: &[]int64{0}[0],
					},
					Env: []corev1.EnvVar{
						{
							Name:  "IMAGETEST",
							Value: "true",
						},
						{
							Name:  "IMAGETEST_DRIVER",
							Value: "k3s_in_docker",
						},
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
					WorkingDir:             "/imagetest",
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

	pobj, err := k.kcli.CoreV1().Pods(k.Namespace).Create(ctx, pod, metav1.CreateOptions{})
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
		lreq := k.kcli.CoreV1().Pods(k.Namespace).GetLogs(pobj.Name, &corev1.PodLogOptions{Follow: true, Container: "sandbox"})
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
					plog.InfoContext(ctx, "received pod log line", "message", line)
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

			plog.InfoContext(ctx, "received event", "message", e.Message, "reason", e.Reason, "name", e.Name)

			if e.Reason == string(corev1.ResourceHealthStatusUnhealthy) && started && strings.Contains(e.Message, "Readiness probe failed") {
				// this filters out "Readiness probe errored" events, which are always
				// fired after a pod successfully completes (0/1 Completed)
				plog.InfoContext(ctx, "test sandbox pod failed and is paused in debug mode")
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
				Namespace: k.Namespace,
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
	ns, err := k.kcli.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: k.Namespace,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// Create the relevant rbac
	sa, err := k.kcli.CoreV1().ServiceAccounts(ns.Name).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "imagetest",
			Namespace: ns.Name,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create service account: %w", err)
	}

	// Create the role binding
	_, err = k.kcli.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "imagetest",
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
		return fmt.Errorf("failed to create role binding: %w", err)
	}

	return nil
}
