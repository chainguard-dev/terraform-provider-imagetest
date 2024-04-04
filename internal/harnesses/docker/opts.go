package docker

import (
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/container"
	"github.com/google/go-containerregistry/pkg/name"
)

type HarnessDockerOptions struct {
	ImageRef       name.Reference
	ManagedVolumes []container.ConfigMount
	Networks       []string
	Mounts         []container.ConfigMount
	SocketPath     string
	Envs           provider.Env
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

func WithSocketPath(socketPath string) Option {
	return func(opt *HarnessDockerOptions) error {
		opt.SocketPath = socketPath
		return nil
	}
}
