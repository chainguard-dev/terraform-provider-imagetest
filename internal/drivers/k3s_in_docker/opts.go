package k3sindocker

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

type DriverOpts func(*driver) error

func WithImageRef(rawRef string) DriverOpts {
	return func(k *driver) error {
		ref, err := name.ParseReference(rawRef)
		if err != nil {
			return err
		}
		k.ImageRef = ref
		return nil
	}
}

func WithCNI(enabled bool) DriverOpts {
	return func(k *driver) error {
		k.CNI = enabled
		return nil
	}
}

func WithTraefik(enabled bool) DriverOpts {
	return func(k *driver) error {
		k.Traefik = enabled
		return nil
	}
}

func WithMetricsServer(enabled bool) DriverOpts {
	return func(k *driver) error {
		k.MetricsServer = enabled
		return nil
	}
}

func WithNetworkPolicy(enabled bool) DriverOpts {
	return func(k *driver) error {
		k.NetworkPolicy = enabled
		return nil
	}
}

func WithSnapshotter(snapshotter string) DriverOpts {
	return func(k *driver) error {
		k.Snapshotter = snapshotter
		return nil
	}
}

func WithRegistry(registry string) DriverOpts {
	return func(k *driver) error {
		if k.Registries == nil {
			k.Registries = make(map[string]*K3sRegistryConfig)
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

		k.Registries[registry] = &K3sRegistryConfig{
			Auth: &K3sRegistryAuthConfig{
				Username: acfg.Username,
				Password: acfg.Password,
				Auth:     acfg.Auth,
			},
		}

		return nil
	}
}

func WithWriteKubeconfig(path string) DriverOpts {
	return func(k *driver) error {
		k.kubeconfigWritePath = path
		return nil
	}
}

func WithRegistryMirror(registry string, endpoints ...string) DriverOpts {
	return func(k *driver) error {
		if k.Registries == nil {
			k.Registries = make(map[string]*K3sRegistryConfig)
		}

		if _, ok := k.Registries[registry]; !ok {
			k.Registries[registry] = &K3sRegistryConfig{}
		}

		k.Registries[registry].Mirrors = &K3sRegistryMirrorConfig{
			Endpoints: endpoints,
		}

		return nil
	}
}

func WithPostStartHook(hook string) DriverOpts {
	return func(k *driver) error {
		if k.Hooks == nil {
			k.Hooks = &K3sHooks{}
		}
		if k.Hooks.PostStart == nil {
			k.Hooks.PostStart = make([]string, 0)
		}
		k.Hooks.PostStart = append(k.Hooks.PostStart, hook)
		return nil
	}
}
