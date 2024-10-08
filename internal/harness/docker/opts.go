package docker

import (
	"fmt"

	client "github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

type Option func(*dind) error

type VolumeConfig struct {
	Name   string
	Target string
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

func WithName(name string) Option {
	return func(opt *dind) error {
		opt.Name = name
		return nil
	}
}

func WithImageRef(ref name.Reference) Option {
	return func(opt *dind) error {
		opt.ImageRef = ref
		return nil
	}
}

func WithMounts(mounts ...mount.Mount) Option {
	return func(opt *dind) error {
		if mounts != nil {
			opt.Mounts = append(opt.Mounts, mounts...)
		}
		return nil
	}
}

func WithNetworks(networks ...client.NetworkAttachment) Option {
	return func(opt *dind) error {
		opt.Networks = append(opt.Networks, networks...)
		return nil
	}
}

func WithAuthFromStatic(registry, username, password, auth string) Option {
	return func(opt *dind) error {
		if opt.Registries == nil {
			opt.Registries = make(map[string]*RegistryConfig)
		}
		if _, ok := opt.Registries[registry]; !ok {
			opt.Registries[registry] = &RegistryConfig{}
		}

		opt.Registries[registry].Auth = &RegistryAuthConfig{
			Username: username,
			Password: password,
			Auth:     auth,
		}

		return nil
	}
}

func WithAuthFromKeychain(registry string) Option {
	return func(opt *dind) error {
		if opt.Registries == nil {
			opt.Registries = make(map[string]*RegistryConfig)
		}
		if _, ok := opt.Registries[registry]; !ok {
			opt.Registries[registry] = &RegistryConfig{}
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

		opt.Registries[registry].Auth = &RegistryAuthConfig{
			Username: acfg.Username,
			Password: acfg.Password,
			Auth:     acfg.Auth,
		}

		return nil
	}
}

func WithEnvs(env ...string) Option {
	return func(opt *dind) error {
		if opt.Envs == nil {
			opt.Envs = make([]string, 0)
		}
		opt.Envs = append(opt.Envs, env...)
		return nil
	}
}

func WithResources(req client.ResourcesRequest) Option {
	return func(opt *dind) error {
		opt.Resources = req
		return nil
	}
}

func WithVolumes(volumes ...VolumeConfig) Option {
	return func(opt *dind) error {
		if volumes == nil {
			return nil
		}
		opt.Volumes = append(opt.Volumes, volumes...)
		return nil
	}
}
