package k3s

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/tools/clientcmd"
)

var _ harness.Harness = &k3s{}

type k3s struct {
	Service *serviceConfig
	Sandbox *docker.Request

	Hooks Hooks

	HostPort           int
	HostKubeconfigPath string

	stack  *harness.Stack
	runner func(context.Context, harness.Command) error
}

func New(opts ...Option) (*k3s, error) {
	h := &k3s{
		Service: &serviceConfig{
			Ref:           name.MustParseReference("cgr.dev/chainguard/k3s:latest"),
			Cni:           true,
			Traefik:       false,
			MetricsServer: false,
			NetworkPolicy: false,
			// Default to the bare minimum k3s needs to run properly
			// https://docs.k3s.io/installation/requirements#hardware
			Resources: docker.ResourcesRequest{
				MemoryRequest: resource.MustParse("2Gi"),
				CpuRequest:    resource.MustParse("1"),
			},
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

	vol, err := cli.CreateVolume(ctx, &docker.VolumeRequest{
		Target: "/etc/rancher/k3s",
	})
	if err != nil {
		return fmt.Errorf("creating volume: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.RemoveVolume(ctx, vol)
	}); err != nil {
		return fmt.Errorf("adding volume teardown to stack: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)

	// Spin up the k3s service
	g.Go(func() error {
		name := h.Service.Name
		if name == "" {
			// We need a deterministic name for the clusters tls-san
			name = uuid.New().String()
		}

		contents := []*docker.Content{}

		cfg, err := h.config(name)
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

		resp, err := cli.Start(ctx, &docker.Request{
			Name:       name,
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
				vol,
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
				Test:          []string{"CMD", "/bin/sh", "-c", "k3s kubectl get --raw='/healthz'"},
				Interval:      1 * time.Second,
				Timeout:       5 * time.Second,
				Retries:       5,
				StartInterval: 1 * time.Second,
			},
			Contents:  contents,
			Resources: h.Service.Resources,
		})
		if err != nil {
			return fmt.Errorf("starting k3s service: %w", err)
		}

		if err := h.stack.Add(func(ctx context.Context) error {
			return cli.Remove(ctx, resp)
		}); err != nil {
			return fmt.Errorf("adding k3s service teardown to stack: %w", err)
		}

		// Modify the kubeconfig to use the containers external hostname, this
		// makes it accessible from the host and any network attached container
		if err := resp.Run(ctx, harness.Command{
			Args: fmt.Sprintf("KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl config set-cluster default --server https://%[1]s:6443 > /dev/null", resp.Name),
		}); err != nil {
			return fmt.Errorf("setting cluster name: %w", err)
		}

		if h.HostKubeconfigPath != "" {
			var kr bytes.Buffer
			if err := resp.Run(ctx, harness.Command{
				Args:   "KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl config view --raw > /dev/null",
				Stdout: &kr,
			}); err != nil {
				return fmt.Errorf("writing kubeconfig to host: %w", err)
			}

			data, err := io.ReadAll(&kr)
			if err != nil {
				return fmt.Errorf("reading kubeconfig from host: %w", err)
			}

			cfg, err := clientcmd.Load(data)
			if err != nil {
				return fmt.Errorf("loading kubeconfig: %w", err)
			}

			_, ok := cfg.Clusters["default"]
			if !ok {
				return fmt.Errorf("no default context found in kubeconfig")
			}
			cfg.Clusters["default"].Server = fmt.Sprintf("https://127.0.0.1:%d", h.HostPort)

			if err := clientcmd.WriteToFile(*cfg, h.HostKubeconfigPath); err != nil {
				return fmt.Errorf("writing kubeconfig to host: %w", err)
			}
		}

		// Run the post start hooks
		for _, hook := range h.Hooks.PostStart {
			if err := resp.Run(ctx, harness.Command{
				Args: hook,
			}); err != nil {
				return fmt.Errorf("running post start hook: %w", err)
			}
		}

		return nil
	})

	// Create the sandbox
	g.Go(func() error {
		req := h.Sandbox
		req.Mounts = append(req.Mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: vol.Source,
			Target: "/k3s-config",
		})
		req.Networks = append(req.Networks, docker.NetworkAttachment{
			Name: nw.Name,
			ID:   nw.ID,
		})

		sandbox, err := cli.Start(ctx, req)
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
	})

	if err := g.Wait(); err != nil {
		return err
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
