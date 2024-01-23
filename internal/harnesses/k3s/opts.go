package k3s

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

type Opt struct {
	Image         string
	Traefik       bool
	Cni           bool
	MetricsServer bool
	Networks      []string

	Registries map[string]*RegistryOpt
	Mirrors    map[string]*RegistryMirrorOpt
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

type RegistryMirrorOpt struct {
	Endpoints []string
}

type Option func(*Opt) error

func WithImage(image string) Option {
	return func(opt *Opt) error {
		opt.Image = image
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

		opt.Registries[registry].Auth = &RegistryAuthOpt{
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

		opt.Registries[registry].Auth = &RegistryAuthOpt{
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
		return nil
	}
}
