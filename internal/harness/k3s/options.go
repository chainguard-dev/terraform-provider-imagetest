package k3s

import (
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

type Option func(*k3s) error

type serviceConfig struct {
	Name          string
	Ref           name.Reference
	Traefik       bool
	Cni           bool
	MetricsServer bool
	NetworkPolicy bool
	KubeletConfig string
	Snapshotter   Snapshotter
	Registries    map[string]*RegistryConfig
	Mirrors       map[string]*MirrorConfig
	Resources     docker.ResourcesRequest
	Networks      []docker.NetworkAttachment // A list of existing networks names (or network aliases) to attach the harness containers to.
}

type RegistryConfig struct {
	Auth *RegistryAuthConfig
	Tls  *RegistryTlsConfig
}

type RegistryAuthConfig struct {
	Username string
	Password string
	Auth     string
}

type RegistryTlsConfig struct {
	CertFile string
	KeyFile  string
	CaFile   string
}

type MirrorConfig struct {
	Endpoints []string
}

// Hooks are the hooks that can be run at various stages of the k3s lifecycle.
type Hooks struct {
	// PreStart is a list of commands to run after the k3s container successfully
	// starts (the api server is available).
	PostStart []string
}

type Snapshotter string

const (
	K3sContainerSnapshotterNative    Snapshotter = "native"
	K3sContainerSnapshotterOverlayfs Snapshotter = "overlayfs"
)

func WithName(name string) Option {
	return func(h *k3s) error {
		h.Service.Name = name
		return nil
	}
}

func WithImageRef(ref name.Reference) Option {
	return func(h *k3s) error {
		h.Service.Ref = ref
		return nil
	}
}

// WithCniDisabled disables the CNI plugin.
func WithCniDisabled(disabled bool) Option {
	return func(h *k3s) error {
		h.Service.Cni = !disabled
		return nil
	}
}

// WithTraefikDisabled disables the traefik ingress controller.
func WithTraefikDisabled(disabled bool) Option {
	return func(h *k3s) error {
		h.Service.Traefik = !disabled
		return nil
	}
}

// WithMetricsServerDisabled disables the metrics server.
func WithMetricsServerDisabled(disabled bool) Option {
	return func(h *k3s) error {
		h.Service.MetricsServer = !disabled
		return nil
	}
}

func WithNetworkPolicyDisabled(disabled bool) Option {
	return func(h *k3s) error {
		h.Service.NetworkPolicy = !disabled
		return nil
	}
}

func WithAuthFromStatic(registry, username, password, auth string) Option {
	return func(h *k3s) error {
		if h.Service.Registries == nil {
			h.Service.Registries = make(map[string]*RegistryConfig)
		}
		if _, ok := h.Service.Registries[registry]; !ok {
			h.Service.Registries[registry] = &RegistryConfig{}
		}

		h.Service.Registries[registry].Auth = &RegistryAuthConfig{
			Username: username,
			Password: password,
			Auth:     auth,
		}

		return nil
	}
}

func WithAuthFromKeychain(registry string) Option {
	return func(h *k3s) error {
		if h.Service.Registries == nil {
			h.Service.Registries = make(map[string]*RegistryConfig)
		}
		if _, ok := h.Service.Registries[registry]; !ok {
			h.Service.Registries[registry] = &RegistryConfig{}
		}

		r, err := name.NewRegistry(registry)
		if err != nil {
			return fmt.Errorf("invalid registry name: %w", err)
		}

		a, err := authn.DefaultKeychain.Resolve(r)
		if err != nil {
			return fmt.Errorf("resolving keychain for registry %s: %w", r.String(), err)
		}

		acfg, err := a.Authorization()
		if err != nil {
			return fmt.Errorf("getting authorization for registry %s: %w", r.String(), err)
		}

		h.Service.Registries[registry].Auth = &RegistryAuthConfig{
			Username: acfg.Username,
			Password: acfg.Password,
			Auth:     acfg.Auth,
		}

		return nil
	}
}

func WithResources(req docker.ResourcesRequest) Option {
	return func(opt *k3s) error {
		opt.Service.Resources = req
		return nil
	}
}

func WithRegistryMirror(registry string, endpoints ...string) Option {
	return func(opt *k3s) error {
		if opt.Service.Mirrors == nil {
			opt.Service.Mirrors = make(map[string]*MirrorConfig)
		}
		opt.Service.Mirrors[registry] = &MirrorConfig{
			Endpoints: endpoints,
		}
		return nil
	}
}

func WithSnapshotter(snapshotter Snapshotter) Option {
	return func(opt *k3s) error {
		opt.Service.Snapshotter = snapshotter
		return nil
	}
}

func WithHooks(hooks Hooks) Option {
	return func(opt *k3s) error {
		opt.Hooks = hooks
		return nil
	}
}

func WithKubeletConfig(kubeletConfig string) Option {
	return func(opt *k3s) error {
		config := new(kubeletconfigv1beta1.KubeletConfiguration)
		scheme := runtime.NewScheme()
		err := kubeletconfigv1beta1.AddToScheme(scheme)
		if err != nil {
			return fmt.Errorf("failed to add k8s type to scheme: %w", err)
		}

		codec := serializer.NewCodecFactory(scheme)
		_, _, err = codec.UniversalDeserializer().Decode([]byte(kubeletConfig), nil, config)
		if err != nil {
			return fmt.Errorf("failed to unmarshal configuration: %w", err)
		}

		opt.Service.KubeletConfig = kubeletConfig
		return nil
	}
}

func WithHostPort(port int) Option {
	return func(o *k3s) error {
		o.HostPort = port
		return nil
	}
}

func WithHostKubeconfigPath(path string) Option {
	return func(o *k3s) error {
		o.HostKubeconfigPath = path
		return nil
	}
}

func WithNetworks(networks ...docker.NetworkAttachment) Option {
	return func(opt *k3s) error {
		if opt.Service.Networks == nil {
			opt.Service.Networks = make([]docker.NetworkAttachment, 0)
		}
		opt.Service.Networks = append(opt.Service.Networks, networks...)

		// also append to sandbox networks
		if opt.Sandbox.Networks == nil {
			opt.Sandbox.Networks = make([]docker.NetworkAttachment, 0)
		}
		opt.Sandbox.Networks = append(opt.Sandbox.Networks, networks...)
		return nil
	}
}

func WithSandboxImageRef(ref name.Reference) Option {
	return func(opt *k3s) error {
		opt.Sandbox.Ref = ref
		return nil
	}
}

func WithSandboxMounts(mounts ...mount.Mount) Option {
	return func(opt *k3s) error {
		if opt.Sandbox.Mounts == nil {
			opt.Sandbox.Mounts = []mount.Mount{}
		}
		opt.Sandbox.Mounts = append(opt.Sandbox.Mounts, mounts...)
		return nil
	}
}

func WithSandboxNetworks(networks ...docker.NetworkAttachment) Option {
	return func(opt *k3s) error {
		if opt.Sandbox.Networks == nil {
			opt.Sandbox.Networks = make([]docker.NetworkAttachment, 0)
		}
		opt.Sandbox.Networks = append(opt.Sandbox.Networks, networks...)
		return nil
	}
}

func WithSandboxEnv(envs ...string) Option {
	return func(opt *k3s) error {
		if opt.Sandbox.Env == nil {
			opt.Sandbox.Env = make([]string, 0)
		}
		opt.Sandbox.Env = append(opt.Sandbox.Env, envs...)
		return nil
	}
}

func WithSandboxResources(req docker.ResourcesRequest) Option {
	return func(opt *k3s) error {
		opt.Sandbox.Resources = req
		return nil
	}
}

func WithSandboxName(name string) Option {
	return func(opt *k3s) error {
		opt.Sandbox.Name = name + "-sandbox"
		return nil
	}
}
