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
	"text/template"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	Registries    map[string]K3sRegistryConfig
	Namespace     string // The namespace to use for the test pods

	kubeconfigWritePath string // When set, the generated kubeconfig will be written to this path on the host

	name  string
	stack *harness.Stack
	kcli  kubernetes.Interface
}

type K3sRegistryConfig struct {
	Auth *K3sRegistryAuthConfig
}

type K3sRegistryAuthConfig struct {
	Username string
	Password string
	Auth     string
}

func NewDriver(n string, opts ...DriverOpts) (drivers.Tester, error) {
	k := &driver{
		ImageRef:      name.MustParseReference("cgr.dev/chainguard/k3s:latest"),
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
configs:
  {{- range $k, $v := .Registries }}
  "{{ $k }}":
    auth:
      username: "{{ $v.Auth.Username }}"
      password: "{{ $v.Auth.Password }}"
      auth: "{{ $v.Auth.Auth }}"
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

	clog.InfoContext(ctx, "starting k3s container in docker",
		"image_ref", k.ImageRef.String(),
		"network_id", nw.ID,
	)

	resp, err := cli.Start(ctx, &docker.Request{
		Name:       k.name,
		Ref:        k.ImageRef,
		Cmd:        []string{"server"},
		Privileged: true, // This doesn't work without privilege, so don't make it configurable
		Networks: []docker.NetworkAttachment{{
			Name: nw.Name,
			ID:   nw.ID,
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
		return fmt.Errorf("starting k3s container: %w", err)
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
					Env: []corev1.EnvVar{
						{
							Name:  "IMAGETEST",
							Value: "true",
						},
					},
					WorkingDir: "/imagetest",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "kube-api-access",
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							ReadOnly:  true,
						},
					},
				},
				// TODO: Helper sidecar for logging
				// TODO: Helper sidecar for uploading test artifacts
			},
		},
	}

	clog.InfoContext(ctx, "creating k3s_in_docker test sandbox pod", "pod_name", pod.Name, "pod_namespace", pod.Namespace)
	pobj, err := k.kcli.CoreV1().Pods(k.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create pod: %w", err)
	}

	// watch the pod status
	pw, err := k.kcli.CoreV1().Pods(pobj.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", pobj.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch pod: %w", err)
	}
	defer pw.Stop()

	running := false
	for !running {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-pw.ResultChan():
			if !ok {
				return fmt.Errorf("channel closed")
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				return fmt.Errorf("unexpected watch event type: %T", event.Object)
			}

			if event.Type == watch.Deleted {
				return fmt.Errorf("pod was deleted before becoming ready")
			}

			if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodUnknown {
				return fmt.Errorf("pod failed to start")
			}

			for _, status := range pod.Status.ContainerStatuses {
				if status.Name == "sandbox" {
					if status.State.Waiting == nil {
						running = true
						clog.InfoContext(ctx, "test sandbox pod scheduled", "pod_name", pobj.Name, "pod_namespace", pobj.Namespace, "status", pod.Status.Phase)
						break
					}
				}
			}

			clog.InfoContext(ctx, "waiting for test sandbox pod to schedule", "pod_name", pobj.Name, "pod_namespace", pobj.Namespace, "status", pod.Status.Phase)
		}
	}

	lreq := k.kcli.CoreV1().Pods(k.Namespace).GetLogs(pobj.Name, &corev1.PodLogOptions{Follow: true, Container: "sandbox"})
	logs, err := lreq.Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to stream logs: %w", err)
	}
	defer logs.Close()

	logsDoneCh := make(chan error)

	go func() {
		defer close(logsDoneCh)
		r := bufio.NewReader(logs)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					return
				}
				logsDoneCh <- fmt.Errorf("streaming logs: %w", err)
			}
			log.Info(ctx, string(line), "pod", pobj.Name)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for pod completion: %w", ctx.Err())
		case event, ok := <-pw.ResultChan():
			if !ok {
				return fmt.Errorf("pod watch channel closed unexpectedly")
			}
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			if pod.Status.Phase == corev1.PodSucceeded {
				clog.InfoContext(ctx, "pod successfully completed", "pod", pobj.Name)
				return nil
			}

			if pod.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("pod %s/%s exited with failure", pobj.Name, pobj.Namespace)
			}

			clog.InfoContext(ctx, "waiting for pod to complete", "pod", pobj.Name, "status", pod.Status.Phase)
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
