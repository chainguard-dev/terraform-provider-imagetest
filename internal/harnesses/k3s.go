package harnesses

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"

	"golang.org/x/sync/errgroup"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type K3sConfig struct {
	Image         string
	Traefik       bool
	Cni           bool
	MetricsServer bool
}

type k3s struct {
	*base

	Config K3sConfig
	id     string

	// service is the provider that is running the service, which is k3s in a
	// container
	service provider.Provider
	// sandbox is the provider where the user defined steps will execute. it is
	// wired into the service
	sandbox provider.Provider
}

func NewK3s(id string, cfg K3sConfig) (types.Harness, error) {
	k3s := &k3s{
		base:   NewBase(),
		Config: cfg,
		id:     id,
	}

	ref, err := name.ParseReference(cfg.Image)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	svcName := id + "-service"
	service, err := provider.NewDocker(svcName, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Image:      ref.Name(),
			Cmd:        []string{"server"},
			Privileged: true,
			Files: []provider.File{
				{
					Contents: bytes.NewBufferString(k3s.config(svcName)),
					Target:   "/etc/rancher/k3s/config.yaml",
					Mode:     0644,
				},
			},
		},
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: id + "-config",
				Target: "/etc/rancher/k3s",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	sandbox, err := provider.NewDocker(id+"-sandbox", provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			// TODO: Dynamically build this with predetermined apks
			Image:      "cgr.dev/chainguard/kubectl:latest-dev",
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{"tail -f /dev/null"},
			Networks:   []string{svcName},
			Env: map[string]string{
				"KUBECONFIG": "/k3s-config/k3s.yaml",
			},
			// TODO: Not needed with wolfi-base images
			User: "0:0",
		},
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: id + "-config",
				Target: "/k3s-config",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	k3s.service = service
	k3s.sandbox = sandbox

	return k3s, nil
}

// Setup implements types.Harness.
func (h *k3s) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		g, ctx := errgroup.WithContext(ctx)

		// Start the k3s service
		g.Go(func() error {
			if err := h.service.Start(ctx); err != nil {
				return fmt.Errorf("starting k3s service: %w", err)
			}

			// Wait for the k3s cluster to be ready
			// TODO: Replace this with proper wait.Conditions, this will be ~50% faster if we just scan the startup logs looking for containerd's ready message
			if _, err := h.service.Exec(ctx, `
tries=0; while ! k3s kubectl wait --for condition=ready nodes --all --timeout 120s && [ $tries -lt 30 ]; do sleep 2; tries=$((tries+1)); done
tries=0; while ! k3s kubectl wait --for condition=ready pods --all -n kube-system --timeout 120s && [ $tries -lt 30 ]; do sleep 2; tries=$((tries+1)); done
      `); err != nil {
				return fmt.Errorf("waiting for k3s cluster: %w", err)
			}

			// Move the kubeconfig into place, and give it the appropriate endpoint
			if _, err := h.service.Exec(ctx, fmt.Sprintf(`
KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl config set-cluster default --server "https://%[1]s:6443" > /dev/null
        `, h.id+"-service")); err != nil {
				return fmt.Errorf("creating kubeconfig: %w", err)
			}

			return nil
		})

		g.Go(func() error {
			// Start the sandbox
			if err := h.sandbox.Start(ctx); err != nil {
				return fmt.Errorf("starting sandbox: %w", err)
			}
			return nil
		})

		if err := g.Wait(); err != nil {
			return ctx, err
		}

		return ctx, nil
	})
}

// Destroy implements types.Harness.
func (h *k3s) Destroy(ctx context.Context) error {
	var errs []error
	if err := h.sandbox.Teardown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("tearing down sandbox: %w", err))
	}

	if err := h.service.Teardown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("tearing down service: %w", err))
	}

	if len(errs) > 0 {
		var err error
		for _, e := range errs {
			err = fmt.Errorf("%w: %v", err, e)
		}
		return err
	}

	return nil
}

// StepFn implements types.Harness.
func (h *k3s) StepFn(command string) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		r, err := h.sandbox.Exec(ctx, command)
		if err != nil {
			return ctx, err
		}

		out, err := io.ReadAll(r)
		if err != nil {
			return ctx, err
		}

		tflog.Info(ctx, "Executing step...", map[string]interface{}{
			"command": command,
			"out":     string(out),
		})

		return ctx, nil
	}
}

func (h *k3s) ref() (string, error) {
	return "", nil
}

func (h *k3s) config(hostname string) string {
	// who needs an an api when you have yaml and gotemplates!11!
	// TODO: This is where we'd also handle auth and mirroring
	cfgtmpl := fmt.Sprintf(`
tls-san: "%[1]s"
disable:
{{- if not .Traefik }}
  - traefik
{{- end }}
{{- if not .MetricsServer }}
  - metrics-server
{{- end }}
{{- if not .Cni }}
flannel-backend: none
{{- end }}
`, hostname)

	tmpl, err := template.New("config").Parse(cfgtmpl)
	if err != nil {
		return ""
	}

	buf := &bytes.Buffer{}
	if err := tmpl.Execute(buf, h.Config); err != nil {
		return ""
	}

	return buf.String()
}
