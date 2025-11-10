package dockerindocker

import (
	"maps"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
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
		if d.cliCfg == nil {
			d.cliCfg = &docker.DockerConfig{
				Auths: make(map[string]docker.DockerAuthConfig),
			}
		}

		if d.cliCfg.Auths == nil {
			d.cliCfg.Auths = make(map[string]docker.DockerAuthConfig)
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

		d.cliCfg.Auths[registry] = docker.DockerAuthConfig{
			Username: acfg.Username,
			Password: acfg.Password,
			Auth:     acfg.Auth,
		}

		return nil
	}
}

func WithExtraHosts(hosts ...string) DriverOpts {
	return func(d *driver) error {
		if d.ExtraHosts == nil {
			d.ExtraHosts = make([]string, 0)
		}
		d.ExtraHosts = append(d.ExtraHosts, hosts...)
		return nil
	}
}

func WithExtraEnvs(envs map[string]string) DriverOpts {
	return func(d *driver) error {
		if d.Envs == nil {
			d.Envs = make(map[string]string)
		}
		maps.Copy(d.Envs, envs)
		return nil
	}
}

func WithRegistryMirrors(mirrors ...string) DriverOpts {
	return func(d *driver) error {
		if d.daemonCfg == nil {
			d.daemonCfg = &daemonConfig{
				Mirrors: make([]string, 0),
			}
		}

		if d.daemonCfg.Mirrors == nil {
			d.daemonCfg.Mirrors = make([]string, 0)
		}

		d.daemonCfg.Mirrors = append(d.daemonCfg.Mirrors, mirrors...)
		return nil
	}
}
