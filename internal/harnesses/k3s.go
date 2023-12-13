package harnesses

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"text/template"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/envs"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ types.Harness = &K3s{}

type K3s struct {
	*nonidempotentBase

	name  string
	ports *envs.Ports
	cfg   K3sConfig
}

type K3sConfig struct {
	Image                string
	Version              string
	DisableCni           bool
	DisableTraefik       bool
	DisableMetricsServer bool
}

func DefaultK3sConfig() K3sConfig {
	return K3sConfig{
		DisableCni:           false,
		DisableTraefik:       true,
		DisableMetricsServer: true,
		Image:                "cgr.dev/chainguard/k3s",
		Version:              "latest",
	}
}

func NewK3s(name string, ports *envs.Ports, cfg K3sConfig) *K3s {
	return &K3s{
		nonidempotentBase: NewBase(),
		name:              name,
		ports:             ports,
		cfg:               cfg,
	}
}

// Setup implements types.Harness.
func (h *K3s) Setup() types.EnvFunc {
	return h.NonidempotentSetup(func(ctx context.Context, cfg types.EnvConfig) (context.Context, error) {
		port, free, err := h.ports.Get(ctx)
		if err != nil {
			return ctx, err
		}
		// remove the port from the internal store, since the port is now either in
		// use or free again
		defer free()

		cfgpath, err := h.setupConfig()
		if err != nil {
			return ctx, fmt.Errorf("writing k3s config: %v", err)
		}

		out := &bytes.Buffer{}
		if err := h.exec(ctx, out, fmt.Sprintf(`
docker run --name 'imagetest-k3s-%[1]s' -d -p %[2]d:6443 --privileged -v %[3]s:/etc/rancher/k3s/config.yaml cgr.dev/chainguard/k3s:latest server
docker exec 'imagetest-k3s-%[1]s' /bin/sh -c 'tries=0; while ! k3s kubectl wait --for condition=ready nodes --all --timeout 120s && [ $tries -lt 30 ]; do sleep 2; tries=$((tries+1)); done'
docker exec 'imagetest-k3s-%[1]s' /bin/sh -c 'tries=0; while ! k3s kubectl wait --for condition=ready pods --all -n kube-system --timeout 120s && [ $tries -lt 30 ]; do sleep 2; tries=$((tries+1)); done'
      `, h.name, port, cfgpath)); err != nil {
			return ctx, fmt.Errorf("creating k3s cluster: %v\n%s", err, out.String())
		}

		kcfg, err := os.CreateTemp("", "imagetest-k3s-kubeconfig")
		if err != nil {
			return ctx, err
		}
		defer kcfg.Close()

		switch e := cfg.(type) {
		case *envs.ExecEnvConfig:
			if err := h.exec(ctx, kcfg, fmt.Sprintf(`
temp=$(mktemp)
trap "rm -f $temp" EXIT

docker exec 'imagetest-k3s-%[1]s' /bin/sh -c "cat /etc/rancher/k3s/k3s.yaml" > $temp
KUBECONFIG=$temp kubectl config set-cluster default --server "https://0.0.0.0:%[2]d" > /dev/null
cat $temp
        `, h.name, port)); err != nil {
				return ctx, fmt.Errorf("populating k3s kubeconfig file: %v", err)
			}

			tflog.Info(ctx, "exec environment populating kubeconfig", map[string]interface{}{
				"kubeconfig": kcfg.Name(),
			})
			e.Envs["KUBECONFIG"] = kcfg.Name()
		}

		return ctx, nil
	})
}

// Destroy implements types.Harness.
func (h *K3s) Destroy(ctx context.Context) error {
	tflog.Info(ctx, "Destroying k3s environment")

	if err := h.exec(ctx, io.Discard, fmt.Sprintf(`
docker rm -f 'imagetest-k3s-%[1]s'
      `, h.name)); err != nil {
		return err
	}

	return nil
}

func (h *K3s) exec(ctx context.Context, w io.Writer, command string) error {
	var buf bytes.Buffer
	defer buf.WriteTo(w)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func (h *K3s) setupConfig() (string, error) {
	f, err := os.CreateTemp("", "imagetest-k3s-config")
	if err != nil {
		return "", nil
	}
	defer f.Close()

	cfgtmpl := `
tls-san: "0.0.0.0"
disable:
{{- if .DisableTraefik }}
  - traefik
{{- end }}
{{- if .DisableMetricsServer }}
  - metrics-server
{{- end }}
{{- if .DisableCni }}
flannel-backend: none
{{- end }}
`

	tmpl, err := template.New("config").Parse(cfgtmpl)
	if err != nil {
		return "", err
	}

	if err := tmpl.Execute(f, h.cfg); err != nil {
		return "", err
	}

	return f.Name(), nil
}
