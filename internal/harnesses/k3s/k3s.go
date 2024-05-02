package k3s

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	K3sImageTag     = "cgr.dev/chainguard/k3s:latest"
	KubectlImageTag = "cgr.dev/chainguard/kubectl:latest-dev"
)

type k3s struct {
	*base.Base
	// opt are the options for the k3s harness
	opt *Opt
	// id is an identifier used to prepend to containers created by this harness
	id string
	// service is the provider that is running the service, which is k3s in a
	// container
	service provider.Provider
	// sandbox is the provider where the user defined steps will execute. it is
	// wired into the service
	sandbox provider.Provider
}

func New(id string, cli *provider.DockerClient, opts ...Option) (types.Harness, error) {
	harnessOptions := &Opt{
		ImageRef:      name.MustParseReference(K3sImageTag),
		Cni:           true,
		MetricsServer: false,
		Snapshotter:   "overlayfs",
		Traefik:       false,
		// Default to the bare minimum k3s needs to run properly
		// https://docs.k3s.io/installation/requirements#hardware
		Resources: provider.ContainerResourcesRequest{
			MemoryRequest: resource.MustParse("2Gi"),
			CpuRequest:    resource.MustParse("1"),
		},
		Sandbox: provider.DockerRequest{
			ContainerRequest: provider.ContainerRequest{
				Ref:        name.MustParseReference(KubectlImageTag),
				Entrypoint: base.DefaultEntrypoint(),
				Cmd:        base.DefaultCmd(),
				Env: map[string]string{
					"KUBECONFIG": "/k3s-config/k3s.yaml",
					"ENV":        "/root/.ashrc",
				},
				Files: []provider.File{
					{
						Contents: bytes.NewBufferString("alias k=kubectl"),
						Target:   "/root/.ashrc",
						Mode:     644,
					},
				},
				User: "0:0",
				// Default to something small just for "scheduling" purposes, the bulk of
				// the work happens in the service container
				Resources: provider.ContainerResourcesRequest{
					MemoryRequest: resource.MustParse("250Mi"),
					CpuRequest:    resource.MustParse("100m"),
				},
			},
		},
	}

	for _, o := range opts {
		if err := o(harnessOptions); err != nil {
			return nil, err
		}
	}

	harnessOptions.Sandbox.Mounts = append(harnessOptions.Sandbox.Mounts, mount.Mount{
		Type:   mount.TypeVolume,
		Source: harnessOptions.ContainerVolumeName,
		Target: "/k3s-config",
	})

	k3s := &k3s{
		Base: base.New(),
		id:   id,
		opt:  harnessOptions,
	}

	kcfg, err := k3s.genConfig()
	if err != nil {
		return nil, fmt.Errorf("creating k3s config: %w", err)
	}

	rcfg, err := k3s.genRegistries()
	if err != nil {
		return nil, fmt.Errorf("creating k3s registries config: %w", err)
	}

	service := provider.NewDocker(id, cli, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Ref:        harnessOptions.ImageRef,
			Cmd:        []string{"server"},
			Privileged: true,
			Networks:   harnessOptions.Networks,
			Files: []provider.File{
				{
					Contents: kcfg,
					Target:   "/etc/rancher/k3s/config.yaml",
					Mode:     0644,
				},
				{
					Contents: rcfg,
					Target:   "/etc/rancher/k3s/registries.yaml",
					Mode:     0644,
				},
			},
			Resources: harnessOptions.Resources,
		},
		ManagedVolumes: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: harnessOptions.ContainerVolumeName,
				Target: "/etc/rancher/k3s",
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
	})

	sandbox := provider.NewDocker(k3s.id+"-sandbox", cli, harnessOptions.Sandbox)

	k3s.service = service
	k3s.sandbox = sandbox

	return k3s, nil
}

// Setup implements types.Harness.
func (h *k3s) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		log := clog.FromContext(ctx)
		g, ctx := errgroup.WithContext(ctx)

		// start the k3s service first since the sandbox depends on the network
		// being created
		if err := h.service.Start(ctx); err != nil {
			return ctx, fmt.Errorf("starting k3s service: %w", err)
		}

		// Wait for the k3s service
		g.Go(func() error {
			// Block until k3s is ready. It is up to the caller to ensure the context
			// is cancelled to stop waiting.
			// TODO: Replace this with a better wait.Conditions, this will be ~50%
			// faster if we just scan the startup logs looking for containerd's ready
			// message
			if _, err := h.service.Exec(ctx, provider.ExecConfig{
				Command: `
while true; do
  if k3s kubectl cluster-info; then
    break
  fi
  sleep 0.5
done
      `,
			}); err != nil {
				return fmt.Errorf("waiting for k3s cluster: %w", err)
			}

			// Move the kubeconfig into place, and give it the appropriate endpoint
			if _, err := h.service.Exec(ctx, provider.ExecConfig{
				Command: fmt.Sprintf(`
KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl config set-cluster default --server "https://%[1]s:6443" > /dev/null
        `, h.id),
			}); err != nil {
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

		log.Infof("Waiting for k3s service to be ready")
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
			if err != nil {
				err = fmt.Errorf("%w: %v", err, e)
			} else {
				err = e
			}
		}
		return err
	}

	return nil
}

// StepFn implements types.Harness.
func (h *k3s) StepFn(config types.StepConfig) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		log := clog.FromContext(ctx)
		log.Infof("stepping in k3s sandbox container with command %s", config.Command)
		r, err := h.sandbox.Exec(ctx, provider.ExecConfig{
			Command:    config.Command,
			WorkingDir: config.WorkingDir,
		})
		if err != nil {
			return ctx, err
		}

		out, err := io.ReadAll(r)
		if err != nil {
			return ctx, err
		}

		log.Infof("finished stepping in k3s sandbox container with command %s, out: %s", config.Command, string(out))

		return ctx, nil
	}
}

func (h *k3s) DebugLogCommand() string {
	return `PODLIST=$(kubectl get pods --all-namespaces --output=go-template='{{ range $pod := .items }}{{ range $status := .status.containerStatuses }}{{ if eq $status.state.waiting.reason "CrashLoopBackOff" }}{{ $pod.metadata.name }} {{ $pod.metadata.namespace }}{{ "\n" }}{{ end }}{{ end }}{{ end }}')

if [ -z "$PODLIST" ]; then
  exit 0
fi

IFS=
for POD in ${PODLIST}; do
  echo $POD | awk '{print "kubectl logs " $1 " --namespace " $2}' | xargs -I{} -t sh -c {}
done

exit 1
`
}

func (h *k3s) genRegistries() (io.Reader, error) {
	// who needs an api when you have yaml and gotemplates!11!
	cfgtmpl := `
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

	tmpl, err := template.New("registry").Parse(cfgtmpl)
	if err != nil {
		return nil, err
	}

	buf := &bytes.Buffer{}
	if err := tmpl.Execute(buf, h.opt); err != nil {
		return nil, err
	}

	return buf, nil
}

func (h *k3s) genConfig() (io.Reader, error) {
	// who needs an an api when you have yaml and gotemplates!11!
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
snapshotter: "%[2]s"
`, h.id, h.opt.Snapshotter)

	tmpl, err := template.New("config").Parse(cfgtmpl)
	if err != nil {
		return nil, err
	}

	buf := &bytes.Buffer{}
	if err := tmpl.Execute(buf, h.opt); err != nil {
		return nil, err
	}

	return buf, nil
}
