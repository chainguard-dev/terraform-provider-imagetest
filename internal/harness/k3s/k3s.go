package k3s

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"strconv"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sandbox"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sandbox/k8s"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/tools/clientcmd"
)

var _ harness.Harness = &k3s{}

type k3s struct {
	Service *serviceConfig
	Sandbox *k8s.Request

	Hooks Hooks

	stack  *harness.Stack
	runner func(context.Context, harness.Command) error
}

func New(opts ...Option) (*k3s, error) {
	uid := uuid.New().String()

	h := &k3s{
		Service: &serviceConfig{
			Name:            uid,
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
		},
		Sandbox: &k8s.Request{
			Request: sandbox.Request{
				Ref:        name.MustParseReference("cgr.dev/chainguard/kubectl:latest-dev"),
				Name:       uid,
				Namespace:  uid,
				Entrypoint: []string{"/bin/sh", "-c"},
				Cmd:        []string{"tail -f /dev/null"},
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
	cli, err := docker.New()
	if err != nil {
		return err
	}

	// Create the network first
	nw, err := cli.CreateNetwork(ctx, &docker.NetworkRequest{})
	if err != nil {
		return fmt.Errorf("creating network: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.RemoveNetwork(ctx, nw)
	}); err != nil {
		return fmt.Errorf("adding network teardown to stack: %w", err)
	}

	contents := []*docker.Content{}

	cfg, err := h.config(h.Service.Name)
	if err != nil {
		return err
	}
	contents = append(contents, cfg)

	reg, err := h.registries()
	if err != nil {
		return err
	}
	contents = append(contents, reg)

	if h.Service.KubeletConfig != "" {
		kcfg := docker.NewContentFromString(h.Service.KubeletConfig, "/etc/rancher/k3s/kubelet.yaml")
		contents = append(contents, kcfg)
	}

	checkCmd := "kubectl get --raw='/readyz'"
	if h.Service.Cni {
		checkCmd += " && kubectl get sa default -n default"
	}

	resp, err := cli.Start(ctx, &docker.Request{
		Name:       h.Service.Name,
		Ref:        h.Service.Ref,
		Cmd:        []string{"server"},
		Privileged: true,
		Networks: []docker.NetworkAttachment{
			{
				Name: nw.Name,
				ID:   nw.ID,
			},
		},
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
			// Give up to a minute for the cluster to boot, this is a cheap wait so
			// we can poll often
			Test:          []string{"CMD", "/bin/sh", "-c", checkCmd},
			Interval:      1 * time.Second,
			Timeout:       3 * time.Second,
			Retries:       30,
			StartInterval: 1 * time.Second,
		},
		Contents:  contents,
		Resources: h.Service.Resources,
		PortBindings: nat.PortMap{
			nat.Port(strconv.Itoa(h.Service.HttpsListenPort)): []nat.PortBinding{
				{
					HostIP:   "127.0.0.1",
					HostPort: "", // let the daemon pick a random port
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("starting k3s service: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.Remove(ctx, resp)
	}); err != nil {
		return fmt.Errorf("adding k3s service teardown to stack: %w", err)
	}

	// Run the post start hooks
	for _, hook := range h.Hooks.PostStart {
		if err := resp.Run(ctx, harness.Command{
			Args: hook,
		}); err != nil {
			return fmt.Errorf("running post start hook: %w", err)
		}
	}

	kcfg, err := h.loadKubeconfig(ctx, resp)
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}

	sandbox, err := k8s.NewFromKubeconfig(kcfg, k8s.WithRequest(h.Sandbox))
	if err != nil {
		return fmt.Errorf("creating sandbox: %w", err)
	}

	sbxrunner, err := sandbox.Start(ctx)
	if err != nil {
		return fmt.Errorf("starting sandbox: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return sandbox.Destroy(ctx)
	}); err != nil {
		return fmt.Errorf("adding sandbox teardown to stack: %w", err)
	}

	h.runner = func(ctx context.Context, cmd harness.Command) error {
		return sbxrunner.Run(ctx, cmd)
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

func (h *k3s) config(host string) (*docker.Content, error) {
	tpl := fmt.Sprintf(`
tls-san: "%[1]s"
https-listen-port: {{ .HttpsListenPort }}
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

func (h *k3s) loadKubeconfig(ctx context.Context, resp *docker.Response) ([]byte, error) {
	var kr bytes.Buffer
	if err := resp.Run(ctx, harness.Command{
		Args:   "kubectl config view --raw",
		Stdout: &kr,
	}); err != nil {
		return nil, fmt.Errorf("writing kubeconfig to host: %w", err)
	}

	data, err := io.ReadAll(&kr)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig from host: %w", err)
	}

	cfg, err := clientcmd.Load(data)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	if resp.NetworkSettings == nil && resp.NetworkSettings.Ports == nil {
		return nil, fmt.Errorf("no network settings found")
	}

	apiPortName := nat.Port(strconv.Itoa(h.Service.HttpsListenPort)) + "/tcp"
	ports, ok := resp.NetworkSettings.Ports[apiPortName]
	if !ok {
		return nil, fmt.Errorf("no %s port found", apiPortName)
	}

	if len(ports) == 0 {
		return nil, fmt.Errorf("no %s port found", apiPortName)
	}

	if _, ok := cfg.Clusters["default"]; !ok {
		return nil, fmt.Errorf("no default context found in kubeconfig")
	}

	cfg.Clusters["default"].Server = fmt.Sprintf("https://127.0.0.1:%s", ports[0].HostPort)
	return clientcmd.Write(*cfg)
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
