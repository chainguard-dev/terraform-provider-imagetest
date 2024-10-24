package k3s

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"strconv"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/cli/cli/config/configfile"
	dtypes "github.com/docker/cli/cli/config/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

var _ harness.Harness = &k3s{}

type k3s struct {
	Service *serviceConfig
	Sandbox *docker.Request

	Hooks Hooks

	stack  *harness.Stack
	runner func(context.Context, harness.Command) error

	kcfg *rest.Config
	kcli kubernetes.Interface
}

func New(opts ...Option) (*k3s, error) {
	h := &k3s{
		Service: &serviceConfig{
			Ref:             name.MustParseReference("cgr.dev/chainguard/k3s:latest"),
			Cni:             true,
			Traefik:         false,
			MetricsServer:   false,
			NetworkPolicy:   false,
			HttpsListenPort: 6443,
			// Default to the bare minimum k3s needs to run properly
			// https://docs.k3s.io/installation/requirements#hardware
			Resources: docker.ResourcesRequest{
				MemoryRequest: resource.MustParse("2Gi"),
				CpuRequest:    resource.MustParse("1"),
			},
			Networks: make([]docker.NetworkAttachment, 0),
		},
		Sandbox: &docker.Request{
			Ref:        name.MustParseReference("cgr.dev/chainguard/kubectl:latest-dev"),
			User:       "0:0",
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{"tail -f /dev/null"},
			// Default to something small just for "scheduling" purposes
			Resources: docker.ResourcesRequest{
				MemoryRequest: resource.MustParse("250Mi"),
				CpuRequest:    resource.MustParse("100m"),
			},
			Env: []string{
				"IMAGETEST=true",
				"KUBECONFIG=/k3s-config/k3s.yaml",
			},
			Contents: []*docker.Content{
				docker.NewContentFromString("alias k=kubectl", "/root/.profile"),
			},
			Networks: make([]docker.NetworkAttachment, 0),
			ExtraHosts: []string{
				"host.docker.internal:host-gateway",
			},
		},
		stack: harness.NewStack(),
	}

	for _, opt := range opts {
		if err := opt(h); err != nil {
			return nil, err
		}
	}

	return h, nil
}

// Create implements harness.Harness.
func (h *k3s) Create(ctx context.Context) error {
	// Create the k3s cluster itself

	cli, err := docker.New()
	if err != nil {
		return err
	}

	kresp, err := h.startK3s(ctx, cli)
	if err != nil {
		return fmt.Errorf("starting k3s: %w", err)
	}

	if err := h.startSandbox(ctx, cli, kresp); err != nil {
		return fmt.Errorf("creating sandbox: %w", err)
	}

	return nil
}

// Destroy implements harness.Harness.
func (h *k3s) Destroy(ctx context.Context) error {
	return h.stack.Teardown(ctx)
}

// Run implements harness.Harness.
func (h *k3s) Run(ctx context.Context, cmd harness.Command) error {
	return h.runner(ctx, cmd)
}

func (h *k3s) startK3s(ctx context.Context, cli *docker.Client) (*docker.Response, error) {
	nw, err := cli.CreateNetwork(ctx, &docker.NetworkRequest{})
	if err != nil {
		return nil, fmt.Errorf("creating network: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.RemoveNetwork(ctx, nw)
	}); err != nil {
		return nil, fmt.Errorf("adding network teardown to stack: %w", err)
	}

	name := h.Service.Name
	if name == "" {
		// We need a deterministic name for the clusters tls-san
		name = uuid.New().String()
	}

	contents := []*docker.Content{}

	cfg, err := h.config(name)
	if err != nil {
		return nil, err
	}
	contents = append(contents, cfg)

	reg, err := h.registries()
	if err != nil {
		return nil, err
	}
	contents = append(contents, reg)

	contents = append(contents, docker.NewContentFromString(`
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
- level: Request
  users: ["system:admin"]
  resources:
  - group: ""
    resources: ["*"]
      `, "/var/lib/rancher/k3s/server/audit.yaml"))

	if h.Service.KubeletConfig != "" {
		kcfg := docker.NewContentFromString(h.Service.KubeletConfig, "/etc/rancher/k3s/kubelet.yaml")
		contents = append(contents, kcfg)
	}

	resp, err := cli.Start(ctx, &docker.Request{
		Name:       name,
		Ref:        h.Service.Ref,
		Cmd:        []string{"server"},
		Privileged: true,
		Networks: append(h.Service.Networks, docker.NetworkAttachment{
			Name: nw.Name,
			ID:   nw.ID,
		}),
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeTmpfs,
				Target: "/run",
			},
			{
				Type:   mount.TypeTmpfs,
				Target: "/tmp",
			},
		},
		HealthCheck: &container.HealthConfig{
			Test:          []string{"CMD", "/bin/sh", "-c", "kubectl get --raw='/healthz'"},
			Interval:      1 * time.Second,
			Timeout:       5 * time.Second,
			Retries:       5,
			StartInterval: 1 * time.Second,
		},
		PortBindings: nat.PortMap{
			nat.Port(strconv.Itoa(h.Service.HttpsListenPort)): []nat.PortBinding{
				{
					HostIP:   "127.0.0.1",
					HostPort: "", // Let the daemon pick a random port
				},
			},
		},
		Contents:  contents,
		Resources: h.Service.Resources,
		ExtraHosts: []string{
			"host.docker.internal:host-gateway",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("starting k3s service: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.Remove(ctx, resp)
	}); err != nil {
		return nil, fmt.Errorf("adding k3s service teardown to stack: %w", err)
	}

	kcfg, err := h.kubeconfig(ctx, resp, func(cfg *api.Config) error {
		if resp.NetworkSettings == nil && resp.NetworkSettings.Networks == nil {
			return fmt.Errorf("no network settings found")
		}

		apiPortName := nat.Port(strconv.Itoa(h.Service.HttpsListenPort)) + "/tcp"
		ports, ok := resp.NetworkSettings.Ports[apiPortName]
		if !ok {
			return fmt.Errorf("no host port found for %s", apiPortName)
		}

		if len(ports) == 0 {
			return fmt.Errorf("no host port found for %s", apiPortName)
		}

		cfg.Clusters["default"].Server = fmt.Sprintf("https://127.0.0.1:%s", ports[0].HostPort)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("getting kubeconfig: %w", err)
	}

	h.kcfg, err = clientcmd.RESTConfigFromKubeConfig(kcfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes config: %w", err)
	}

	h.kcli, err = kubernetes.NewForConfig(h.kcfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	// Add the registries auth as a secret to the cluster
	if err := h.registrySecret(ctx); err != nil {
		return nil, fmt.Errorf("adding registry secret: %w", err)
	}

	// Run the post start hooks after we're all done with the cluster setup
	for _, hook := range h.Hooks.PostStart {
		if err := resp.Run(ctx, harness.Command{
			Args: hook,
		}); err != nil {
			return nil, fmt.Errorf("running post start hook: %w", err)
		}
	}

	return resp, nil
}

func (h *k3s) startSandbox(ctx context.Context, cli *docker.Client, resp *docker.Response) error {
	skcfg, err := h.kubeconfig(ctx, resp, func(cfg *api.Config) error {
		cfg.Clusters["default"].Server = fmt.Sprintf("https://%s:%d", resp.Name, h.Service.HttpsListenPort)
		return nil
	})
	if err != nil {
		return fmt.Errorf("getting kubeconfig: %w", err)
	}

	networks := make(map[string]struct{})
	for _, nw := range h.Sandbox.Networks {
		networks[nw.ID] = struct{}{}
	}

	// Attach the sandbox to any networks k3s is also a part of, excluding any
	// invalid networks or networks already attached (the daemon cannot deconflict
	// these)
	for nn, nw := range resp.NetworkSettings.Networks {
		if nn == "" {
			continue
		}
		if _, ok := networks[nn]; !ok {
			h.Sandbox.Networks = append(h.Sandbox.Networks, docker.NetworkAttachment{
				Name: nn,
				ID:   nw.NetworkID,
			})
		}
	}

	h.Sandbox.Name = resp.Name + "-sandbox"

	h.Sandbox.Contents = []*docker.Content{
		docker.NewContentFromString(string(skcfg), "/k3s-config/k3s.yaml"),
	}

	sandbox, err := cli.Start(ctx, h.Sandbox)
	if err != nil {
		return fmt.Errorf("starting sandbox: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.Remove(ctx, sandbox)
	}); err != nil {
		return fmt.Errorf("adding sandbox teardown to stack: %w", err)
	}

	h.runner = func(ctx context.Context, cmd harness.Command) error {
		return sandbox.Run(ctx, cmd)
	}

	return nil
}

func (h *k3s) config(host string) (*docker.Content, error) {
	tpl := fmt.Sprintf(`
tls-san: "%[1]s"
http-listen-port: {{ .HttpsListenPort }}
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
{{- if not .Cni }}
flannel-backend: none
{{- end }}
snapshotter: "%[2]s"
{{- if not (eq .KubeletConfig "") }}
kubelet-arg:
  - config=%[3]s
{{- end }}

kube-apiserver-arg:
  - 'audit-log-path=/var/lib/rancher/k3s/server/logs/audit.log'
  - 'audit-policy-file=/var/lib/rancher/k3s/server/audit.yaml'
    `, host, h.Service.Snapshotter, "/etc/rancher/k3s/kubelet.yaml")

	cfg, err := tmpl(tpl, h.Service)
	if err != nil {
		return nil, err
	}

	return docker.NewContentFromString(cfg, "/etc/rancher/k3s/config.yaml"), nil
}

func (h *k3s) registries() (*docker.Content, error) {
	tpl := `
mirrors:
  {{- range $k, $v := .Mirrors }}
  "{{ $k }}":
    endpoint:
      {{- range $v.Endpoints }}
      - "{{ . }}"
      {{- end }}
  {{- end}}

configs:
  {{- range $k, $v := .Registries }}
  "{{ $k }}":
    auth:
      username: "{{ $v.Auth.Username }}"
      password: "{{ $v.Auth.Password }}"
      auth: "{{ $v.Auth.Auth }}"
    {{- if and $v.Tls $v.Tls.CertFile $v.Tls.KeyFile $v.Tls.CaFile }}
    tls:
      cert_file: "{{ $v.Tls.CertFile }}"
      key_file: "{{ $v.Tls.KeyFile }}"
      ca_file: "{{ $v.Tls.CaFile }}"
    {{- end }}
  {{- end }}
`

	cfg, err := tmpl(tpl, h.Service)
	if err != nil {
		return nil, err
	}

	return docker.NewContentFromString(cfg, "/etc/rancher/k3s/registries.yaml"), nil
}

func (h *k3s) kubeconfig(ctx context.Context, resp *docker.Response, config func(cfg *api.Config) error) ([]byte, error) {
	// Setup host's kube client
	kr, err := resp.GetFile(ctx, "/etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return nil, fmt.Errorf("getting kubeconfig: %w", err)
	}

	krdata, err := io.ReadAll(kr)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig from host: %w", err)
	}

	kcfg, err := clientcmd.Load(krdata)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	if _, ok := kcfg.Clusters["default"]; !ok {
		return nil, fmt.Errorf("no default context found in kubeconfig")
	}

	if err := config(kcfg); err != nil {
		return nil, fmt.Errorf("configuring kubeconfig: %w", err)
	}

	return clientcmd.Write(*kcfg)
}

func (h *k3s) registrySecret(ctx context.Context) error {
	dockerconfig := configfile.ConfigFile{
		AuthConfigs: make(map[string]dtypes.AuthConfig),
	}

	for name, reg := range h.Service.Registries {
		dockerconfig.AuthConfigs[name] = dtypes.AuthConfig{
			Username: reg.Auth.Username,
			Password: reg.Auth.Password,
			Auth:     reg.Auth.Auth,
		}
	}

	dockerConfigJSON, err := json.Marshal(dockerconfig)
	if err != nil {
		return fmt.Errorf("marshaling docker config: %w", err)
	}

	ns := "kube-system"

	_, err = h.kcli.CoreV1().Secrets(ns).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "imagetest-registry-auth",
			Namespace: ns,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			".dockerconfigjson": dockerConfigJSON,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating registry secret: %w", err)
	}

	return nil
}

func tmpl(tpl string, data interface{}) (string, error) {
	t, err := template.New("config").Parse(tpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}
