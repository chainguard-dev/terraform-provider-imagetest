package k3s

import (
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

type Opt struct {
	ImageRef      name.Reference
	Traefik       bool
	Cni           bool
	MetricsServer bool
	NetworkPolicy bool
	Networks      []string
	Resources     provider.ContainerResourcesRequest
	Hooks         Hooks

	// HostPort exposes the clusters apiserver on a given port when set
	HostPort int

	// HostKubeconfigPath writes the clusters kubeconfig to a given path on the
	// host, this is optional and does nothing if not set
	HostKubeconfigPath string

	Registries map[string]*RegistryOpt
	Mirrors    map[string]*RegistryMirrorOpt

	Sandbox             provider.DockerRequest
	ContainerVolumeName string
	Snapshotter         K3sContainerSnapshotter
	KubeletConfig       string
}

// Hooks are the hooks that can be run at various stages of the k3s lifecycle.
type Hooks struct {
	// PreStart is a list of commands to run after the k3s container successfully
	// starts (the api server is available).
	PostStart []string
}

type RegistryOpt struct {
	Auth *base.RegistryAuthOpt
	Tls  *base.RegistryTlsOpt
}

type RegistryMirrorOpt struct {
	Endpoints []string
}

type K3sContainerSnapshotter string

const (
	K3sContainerSnapshotterNative    K3sContainerSnapshotter = "native"
	K3sContainerSnapshotterOverlayfs K3sContainerSnapshotter = "overlayfs"
)

type Option func(*Opt) error

func WithImageRef(ref name.Reference) Option {
	return func(opt *Opt) error {
		opt.ImageRef = ref
		return nil
	}
}

func WithAuthFromStatic(registry, username, password, auth string) Option {
	return func(opt *Opt) error {
		if opt.Registries == nil {
			opt.Registries = make(map[string]*RegistryOpt)
		}
		if _, ok := opt.Registries[registry]; !ok {
			opt.Registries[registry] = &RegistryOpt{}
		}

		opt.Registries[registry].Auth = &base.RegistryAuthOpt{
			Username: username,
			Password: password,
			Auth:     auth,
		}

		return nil
	}
}

func WithAuthFromKeychain(registry string) Option {
	return func(opt *Opt) error {
		if opt.Registries == nil {
			opt.Registries = make(map[string]*RegistryOpt)
		}
		if _, ok := opt.Registries[registry]; !ok {
			opt.Registries[registry] = &RegistryOpt{}
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

		opt.Registries[registry].Auth = &base.RegistryAuthOpt{
			Username: acfg.Username,
			Password: acfg.Password,
			Auth:     acfg.Auth,
		}

		return nil
	}
}

func WithRegistryMirror(registry string, endpoints ...string) Option {
	return func(opt *Opt) error {
		if opt.Mirrors == nil {
			opt.Mirrors = make(map[string]*RegistryMirrorOpt)
		}
		opt.Mirrors[registry] = &RegistryMirrorOpt{
			Endpoints: endpoints,
		}
		return nil
	}
}

func WithNetworks(networks ...string) Option {
	return func(opt *Opt) error {
		if opt.Networks == nil {
			opt.Networks = []string{}
		}
		opt.Networks = append(opt.Networks, networks...)

		// also append to sandbox networks
		if opt.Sandbox.Networks == nil {
			opt.Sandbox.Networks = []string{}
		}
		opt.Sandbox.Networks = append(opt.Sandbox.Networks, networks...)
		return nil
	}
}

func WithContainerSnapshotter(snapshotter K3sContainerSnapshotter) Option {
	return func(opt *Opt) error {
		opt.Snapshotter = snapshotter
		return nil
	}
}

func WithSandboxImageRef(ref name.Reference) Option {
	return func(opt *Opt) error {
		opt.Sandbox.Ref = ref
		return nil
	}
}

func WithSandboxMounts(mounts ...mount.Mount) Option {
	return func(opt *Opt) error {
		if opt.Sandbox.Mounts == nil {
			opt.Sandbox.Mounts = []mount.Mount{}
		}
		opt.Sandbox.Mounts = append(opt.Sandbox.Mounts, mounts...)
		return nil
	}
}

func WithSandboxNetworks(networks ...string) Option {
	return func(opt *Opt) error {
		if opt.Sandbox.Networks == nil {
			opt.Sandbox.Networks = []string{}
		}
		opt.Sandbox.Networks = append(opt.Sandbox.Networks, networks...)
		return nil
	}
}

func WithSandboxEnv(envs provider.Env) Option {
	return func(opt *Opt) error {
		if opt.Sandbox.Env == nil {
			opt.Sandbox.Env = make(provider.Env)
		}

		for k, v := range envs {
			opt.Sandbox.Env[k] = v
		}
		return nil
	}
}

func WithResources(req provider.ContainerResourcesRequest) Option {
	return func(opt *Opt) error {
		opt.Resources = req
		return nil
	}
}

func WithSandboxResources(req provider.ContainerResourcesRequest) Option {
	return func(opt *Opt) error {
		opt.Sandbox.Resources = req
		return nil
	}
}

func WithCniDisabled(disabled bool) Option {
	return func(opt *Opt) error {
		opt.Cni = !disabled
		return nil
	}
}

func WithNetworkPolicyDisabled(disabled bool) Option {
	return func(opt *Opt) error {
		opt.NetworkPolicy = !disabled
		return nil
	}
}

func WithTraefikDisabled(disabled bool) Option {
	return func(opt *Opt) error {
		opt.Traefik = !disabled
		return nil
	}
}

func WithMetricsServerDisabled(disabled bool) Option {
	return func(opt *Opt) error {
		opt.MetricsServer = !disabled
		return nil
	}
}

func WithContainerVolumeName(volumeName string) Option {
	return func(opt *Opt) error {
		opt.ContainerVolumeName = volumeName
		return nil
	}
}

func WithHooks(hooks Hooks) Option {
	return func(opt *Opt) error {
		opt.Hooks = hooks
		return nil
	}
}

// WithHostPort exposes the clusters apiserver on a given port.
func WithHostPort(port int) Option {
	return func(o *Opt) error {
		o.HostPort = port
		return nil
	}
}

func WithHostKubeconfigPath(path string) Option {
	return func(o *Opt) error {
		o.HostKubeconfigPath = path
		return nil
	}
}

func WithKubeletConfig(kubeletConfig string) Option {
	return func(opt *Opt) error {
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

		opt.KubeletConfig = kubeletConfig
		return nil
	}
}
