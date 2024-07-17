package k3s

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/name"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	K3sImageTag       = "cgr.dev/chainguard/k3s:latest"
	KubectlImageTag   = "cgr.dev/chainguard/kubectl:latest-dev"
	KubeletConfigPath = "/etc/rancher/k3s/kubelet.yaml"
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
				Labels: provider.MainHarnessLabel(),
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
		VolumeOptions: &mount.VolumeOptions{
			Labels: provider.DefaultLabels(),
		},
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

	ports := nat.PortMap{}
	if harnessOptions.HostPort > 0 {
		ports = nat.PortMap{
			"6443/tcp": []nat.PortBinding{
				{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", harnessOptions.HostPort)},
			},
		}
	}

	extraFiles := make([]provider.File, 0)
	if "" != k3s.opt.KubeletConfig {
		kubeletConfigBuf := &bytes.Buffer{}
		kubeletConfigBuf.WriteString(k3s.opt.KubeletConfig)

		extraFiles = append(extraFiles, provider.File{
			Contents: kubeletConfigBuf,
			Target:   KubeletConfigPath,
			Mode:     0644,
		})
	}

	service := provider.NewDocker(id, cli, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Ref:        harnessOptions.ImageRef,
			Cmd:        []string{"server"},
			Privileged: true,
			Networks:   harnessOptions.Networks,
			Files: append([]provider.File{
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
			}, extraFiles...),
			Resources: harnessOptions.Resources,
			Ports:     ports,
		},
		ManagedVolumes: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: harnessOptions.ContainerVolumeName,
				Target: "/etc/rancher/k3s",
				VolumeOptions: &mount.VolumeOptions{
					Labels: provider.DefaultLabels(),
				},
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
			// Assume every error is retryable
			if err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
				_, err := h.service.Exec(ctx, provider.ExecConfig{
					Command: "k3s kubectl cluster-info",
				})
				if err != nil {
					log.Info(ctx, "Waiting for k3s service to be ready...", "error", err)
					return false, nil
				}

				return true, nil
			}); err != nil {
				return fmt.Errorf("waiting for k3s service: %w", err)
			}

			// Move the kubeconfig into place, and give it the appropriate endpoint
			if _, err := h.service.Exec(ctx, provider.ExecConfig{
				Command: fmt.Sprintf(`
KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl config set-cluster default --server "https://%[1]s:6443" > /dev/null
        `, h.id),
			}); err != nil {
				return fmt.Errorf("creating kubeconfig: %w", err)
			}

			if h.opt.HostKubeconfigPath != "" {
				log.Info(ctx, "Writing kubeconfig to host", "path", h.opt.HostKubeconfigPath)
				kr, err := h.service.Exec(ctx, provider.ExecConfig{
					Command: `KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl config view --raw &2> /dev/null`,
				})
				if err != nil {
					return fmt.Errorf("writing kubeconfig to host: %w", err)
				}

				data, err := io.ReadAll(kr)
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
				cfg.Clusters["default"].Server = fmt.Sprintf("https://127.0.0.1:%d", h.opt.HostPort)

				if err := clientcmd.WriteToFile(*cfg, h.opt.HostKubeconfigPath); err != nil {
					return fmt.Errorf("writing kubeconfig to host: %w", err)
				}
			}

			// Run the post start hooks
			for _, hook := range h.opt.Hooks.PostStart {
				log.Info(ctx, "K3S Running post start hook", "hook", hook)
				if _, err := h.service.Exec(ctx, provider.ExecConfig{
					Command: hook,
				}); err != nil {
					return fmt.Errorf("running post start hook: %w", err)
				}
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

		log.Info(ctx, "Waiting for k3s service to be ready")
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
		log.Info(ctx, "stepping in k3s sandbox container", "command", config.Command)
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

		log.Info(ctx, "finished stepping in k3s sandbox container", "command", config.Command, "out", string(out))

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
`, h.id, h.opt.Snapshotter, KubeletConfigPath)

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
