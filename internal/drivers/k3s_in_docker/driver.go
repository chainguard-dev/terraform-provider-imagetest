package k3sindocker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/template"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/pod"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
	Namespace     string            // The namespace to use for the test pods
	Hooks         *K3sHooks         // Run commands at various lifecycle events
	SandboxEnvs   map[string]string // Additional environment variables to set in the sandbox

	kubeconfigWritePath string // When set, the generated kubeconfig will be written to this path on the host

	name  string
	stack *harness.Stack
	kcli  kubernetes.Interface
	kcfg  *rest.Config
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

type K3sHooks struct {
	PostStart []string
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
	trace.SpanFromContext(ctx).AddEvent("k3s.network.created")

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
	trace.SpanFromContext(ctx).AddEvent("k3s.container.started")

	kcfgraw, err := resp.ReadFile(ctx, "/etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return fmt.Errorf("getting kubeconfig: %w", err)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig(kcfgraw)
	if err != nil {
		return fmt.Errorf("creating kubernetes config: %w", err)
	}

	// Get port binding - works for both local and SSH
	binding, cleanup, err := resp.PortBinding("6443/tcp")
	if err != nil {
		return fmt.Errorf("getting k3s API port: %w", err)
	}

	// Add cleanup to stack
	if err := k.stack.Add(func(ctx context.Context) error {
		cleanup()
		return nil
	}); err != nil {
		cleanup()
		return fmt.Errorf("adding port cleanup to stack: %w", err)
	}

	config.Host = fmt.Sprintf("https://%s:%s", binding.HostIP, binding.HostPort)
	trace.SpanFromContext(ctx).AddEvent("k3s.kubeconfig.ready")

	kcli, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}
	k.kcli = kcli
	k.kcfg = config

	if k.kubeconfigWritePath != "" {
		kcfg, err := clientcmd.Load(kcfgraw)
		if err != nil {
			return fmt.Errorf("loading kubeconfig: %w", err)
		}

		for _, cluster := range kcfg.Clusters {
			cluster.Server = config.Host
		}

		// Rename the kube context to the name of the test harness.
		// This makes life easier for someone interacting with the test cluster from their host machine
		clog.DebugContext(ctx, "renaming kubeconfig context", "context", k.name)
		kcfg.Contexts[k.name] = kcfg.Contexts["default"]
		kcfg.CurrentContext = k.name

		if err := os.MkdirAll(filepath.Dir(k.kubeconfigWritePath), 0o755); err != nil {
			return fmt.Errorf("failed to create kubeconfig directory: %w", err)
		}

		clog.InfoContext(ctx, "writing kubeconfig to file", "path", k.kubeconfigWritePath)
		if err := clientcmd.WriteToFile(*kcfg, k.kubeconfigWritePath); err != nil {
			return fmt.Errorf("writing kubeconfig: %w", err)
		}
	}

	if err := k.waitReady(ctx); err != nil {
		return fmt.Errorf("waiting for k3s to be ready: %w", err)
	}
	trace.SpanFromContext(ctx).AddEvent("k3s.cluster.ready")

	// Ensure some common mount propagation fixes are applied to make this feel
	// more like a "real" cluster
	defaultMountCommands := []string{
		"mount --make-rshared /",
		"mount --make-rshared /run",
		"mount --make-shared /tmp",
	}

	clog.InfoContext(ctx, "applying default mount propagation settings")
	for _, cmd := range defaultMountCommands {
		if err := resp.Run(ctx, harness.Command{
			Args: cmd,
		}); err != nil {
			clog.WarnContext(ctx, "failed to apply mount propagation", "command", cmd, "error", err)
		}
	}

	// Run user-defined post-start hooks after default setup
	if k.Hooks != nil {
		for _, hook := range k.Hooks.PostStart {
			if err := resp.Run(ctx, harness.Command{
				Args: hook,
			}); err != nil {
				return fmt.Errorf("running post start hook: %w", err)
			}
		}
		trace.SpanFromContext(ctx).AddEvent("k3s.hooks.complete")
	}

	return nil
}

func (k *driver) Teardown(ctx context.Context) error {
	return k.stack.Teardown(ctx)
}

func (k *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	dcfg := &docker.DockerConfig{
		Auths: make(map[string]docker.DockerAuthConfig, len(k.Registries)),
	}
	for reg, cfg := range k.Registries {
		if cfg.Auth == nil {
			continue
		}

		dcfg.Auths[reg] = docker.DockerAuthConfig{
			Username: cfg.Auth.Username,
			Password: cfg.Auth.Password,
			Auth:     cfg.Auth.Auth,
		}
	}

	return pod.Run(ctx, k.kcfg,
		pod.WithImageRef(ref),
		pod.WithExtraEnvs(map[string]string{
			"IMAGETEST_DRIVER": "k3s_in_docker",
		}),
		pod.WithExtraEnvs(k.SandboxEnvs),
		pod.WithRegistryStaticAuth(dcfg),
	)
}

// waitReady blocks until the k3s cluster is "ready". there are many
// definitions of "ready". this one specifically waits for the api server to
// exist, AND for the "default" serviceaccount to exist, which is typically the
// bare requirements for scheduling a workload are. We don't want to wait for
// "kube-system" to be ready, because that typically takes too long, and isn't
// actually required to start scheduling pods.
func (k *driver) waitReady(ctx context.Context) error {
	if _, err := k.kcli.CoreV1().ServiceAccounts("default").Get(ctx, "default", metav1.GetOptions{}); err == nil {
		// sa already exists, we're good to go
		return nil
	}

	saw, err := k.kcli.CoreV1().ServiceAccounts("default").Watch(ctx, metav1.ListOptions{
		Watch:         true,
		FieldSelector: "metadata.name=default",
	})
	if err != nil {
		return fmt.Errorf("watching serviceaccount: %w", err)
	}
	defer saw.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-saw.ResultChan():
			if !ok {
				return fmt.Errorf("service account watcher closed prematurely")
			}

			if e.Object == nil {
				return fmt.Errorf("saw event with nil object")
			}

			if e.Type == watch.Added {
				sa, ok := e.Object.(*corev1.ServiceAccount)
				if ok && sa.Name == "default" {
					// SA created, we're good to go
					return nil
				}
			}
		}
	}
}
