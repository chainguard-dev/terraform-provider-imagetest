package docker

import (
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness/container"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

type HarnessDockerOptions struct {
	ImageRef           name.Reference
	ManagedVolumes     []container.ConfigMount
	Networks           []string
	Mounts             []container.ConfigMount
	HostSocketPath     string
	Envs               provider.Env
	Registries         map[string]*RegistryOpt
	ConfigVolumeName   string
	ContainerResources provider.ContainerResourcesRequest
}

type RegistryOpt struct {
	Auth *RegistryAuthOpt
	Tls  *RegistryTlsOpt
}

type RegistryAuthOpt struct {
	Username string
	Password string
	Auth     string
}

type RegistryTlsOpt struct {
	CertFile string
	KeyFile  string
	CaFile   string
}

type Option func(*HarnessDockerOptions) error

func WithImageRef(ref name.Reference) Option {
	return func(opt *HarnessDockerOptions) error {
		opt.ImageRef = ref
		return nil
	}
}

func WithManagedVolumes(volumes ...container.ConfigMount) Option {
	return func(opt *HarnessDockerOptions) error {
		if volumes != nil {
			opt.ManagedVolumes = append(opt.ManagedVolumes, volumes...)
		}
		return nil
	}
}

func WithMounts(mounts ...container.ConfigMount) Option {
	return func(opt *HarnessDockerOptions) error {
		if mounts != nil {
			opt.Mounts = append(opt.Mounts, mounts...)
		}
		return nil
	}
}

func WithNetworks(networks ...string) Option {
	return func(opt *HarnessDockerOptions) error {
		opt.Networks = append(opt.Networks, networks...)
		return nil
	}
}

func WithAuthFromStatic(registry, username, password, auth string) Option {
	return func(opt *HarnessDockerOptions) error {
		if opt.Registries == nil {
			opt.Registries = make(map[string]*RegistryOpt)
		}
		if _, ok := opt.Registries[registry]; !ok {
			opt.Registries[registry] = &RegistryOpt{}
		}

		opt.Registries[registry].Auth = &RegistryAuthOpt{
			Username: username,
			Password: password,
			Auth:     auth,
		}

		return nil
	}
}

func WithAuthFromKeychain(registry string) Option {
	return func(opt *HarnessDockerOptions) error {
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

		opt.Registries[registry].Auth = &RegistryAuthOpt{
			Username: acfg.Username,
			Password: acfg.Password,
			Auth:     acfg.Auth,
		}

		return nil
	}
}

func WithEnvs(env ...provider.Env) Option {
	return func(opt *HarnessDockerOptions) error {
		if env == nil {
			return nil
		}

		if opt.Envs == nil {
			opt.Envs = make(provider.Env)
		}

		for _, envItem := range env {
			for k, v := range envItem {
				opt.Envs[k] = v
			}
		}

		return nil
	}
}

func WithHostSocketPath(socketPath string) Option {
	return func(opt *HarnessDockerOptions) error {
		opt.HostSocketPath = socketPath
		return nil
	}
}

func WithConfigVolumeName(configVolumeName string) Option {
	return func(opt *HarnessDockerOptions) error {
		opt.ConfigVolumeName = configVolumeName
		return nil
	}
}

func WithContainerResources(request provider.ContainerResourcesRequest) Option {
	return func(opt *HarnessDockerOptions) error {
		opt.ContainerResources = request
		return nil
	}
}
