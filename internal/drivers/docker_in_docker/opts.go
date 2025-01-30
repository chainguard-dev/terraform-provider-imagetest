package dockerindocker

import (
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type DriverOpts func(*driver) error

func WithImageRef(rawRef string) DriverOpts {
	return func(d *driver) error {
		ref, err := name.ParseReference(rawRef)
		if err != nil {
			return err
		}
		d.ImageRef = ref
		return nil
	}
}

func WithRemoteOptions(opts ...remote.Option) DriverOpts {
	return func(d *driver) error {
		if d.ropts == nil {
			d.ropts = make([]remote.Option, 0)
		}
		d.ropts = append(d.ropts, opts...)
		return nil
	}
}

// WithRegistryAuth invokes the docker-credential-helper to exchange static
// creds that are mounted in the container. This current implementation will
// fail if the tests take longer than the tokens ttl.
// TODO: Replace this with Jon's cred proxy: https://gist.github.com/jonjohnsonjr/6d20148edca0f187cfed050cee669685
func WithRegistryAuth(registry string) DriverOpts {
	return func(d *driver) error {
		if d.config == nil {
			d.config = &dockerConfig{
				Auths: make(map[string]dockerAuthEntry),
			}
		}

		if d.config.Auths == nil {
			d.config.Auths = make(map[string]dockerAuthEntry)
		}

		r, err := name.NewRegistry(registry)
		if err != nil {
			return err
		}

		a, err := authn.DefaultKeychain.Resolve(r)
		if err != nil {
			return err
		}

		acfg, err := a.Authorization()
		if err != nil {
			return err
		}

		d.config.Auths[registry] = dockerAuthEntry{
			Username: acfg.Username,
			Password: acfg.Password,
			Auth:     acfg.Auth,
		}

		return nil
	}
}
