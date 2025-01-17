package bundler

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type appendOpts struct {
	ropts  []remote.Option
	layers []Layerer
	envs   []string // A list of extra environment variables to set on the image
}

type AppendOpt func(*appendOpts) error

func AppendWithRemoteOptions(opts ...remote.Option) AppendOpt {
	return func(a *appendOpts) error {
		a.ropts = append(a.ropts, opts...)
		return nil
	}
}

func AppendWithLayers(layers ...Layerer) AppendOpt {
	return func(a *appendOpts) error {
		a.layers = append(a.layers, layers...)
		return nil
	}
}

func AppendWithEnvs(envs map[string]string) AppendOpt {
	return func(a *appendOpts) error {
		if envs == nil {
			a.envs = make([]string, 0)
		}

		for k, v := range envs {
			a.envs = append(a.envs, fmt.Sprintf("%s=%s", k, v))
		}
		return nil
	}
}

func Append(ctx context.Context, base name.Reference, target name.Repository, options ...AppendOpt) (name.Reference, error) {
	opts := &appendOpts{}

	for _, opt := range options {
		if err := opt(opts); err != nil {
			return nil, err
		}
	}

	bundler := &appender{
		base:  base,
		ropts: opts.ropts,
		envs:  opts.envs,
	}

	return bundler.Bundle(ctx, target, opts.layers...)
}

// appender is a bundler that appends layers to existing images,
// copying the base to the target repo.
type appender struct {
	base  name.Reference
	ropts []remote.Option
	envs  []string // A list of extra environment variables to set on the image
}

type AppenderOpt func(*appender) error

func NewAppender(base name.Reference, opts ...AppenderOpt) (Bundler, error) {
	a := &appender{
		base: base,
	}

	for _, opt := range opts {
		if err := opt(a); err != nil {
			return nil, err
		}
	}

	return a, nil
}

func (a *appender) Bundle(ctx context.Context, repo name.Repository, layers ...Layerer) (name.Reference, error) {
	desc, err := remote.Get(a.base, a.ropts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	if desc.MediaType.IsIndex() {
		baseidx, err := desc.ImageIndex()
		if err != nil {
			return nil, fmt.Errorf("failed to get image index: %w", err)
		}

		baseimf, err := baseidx.IndexManifest()
		if err != nil {
			return nil, fmt.Errorf("failed to get image index manifest: %w", err)
		}

		var idx v1.ImageIndex = empty.Index

		for _, manifest := range baseimf.Manifests {
			baseimg, err := baseidx.Image(manifest.Digest)
			if err != nil {
				return nil, fmt.Errorf("failed to load image: %w", err)
			}

			mutated := baseimg
			for _, l := range layers {
				layer, err := l.Layer()
				if err != nil {
					return nil, err
				}

				mutated, err = mutate.AppendLayers(mutated, layer)
				if err != nil {
					return nil, err
				}
			}

			cf, err := mutated.ConfigFile()
			if err != nil {
				return nil, fmt.Errorf("failed to get config file: %w", err)
			}

			cf.Config.Env = append(cf.Config.Env, a.envs...)

			mutated, err = mutate.ConfigFile(mutated, cf)
			if err != nil {
				return nil, fmt.Errorf("failed to set config file: %w", err)
			}

			mdig, err := mutated.Digest()
			if err != nil {
				return nil, fmt.Errorf("failed to get digest: %w", err)
			}

			if err := remote.Write(repo.Digest(mdig.String()), mutated, a.ropts...); err != nil {
				return nil, fmt.Errorf("failed to push image: %w", err)
			}

			// Update the index with the new image
			idx = mutate.AppendManifests(idx, mutate.IndexAddendum{
				Add: mutated,
				Descriptor: v1.Descriptor{
					MediaType:    manifest.MediaType,
					URLs:         manifest.URLs,
					Annotations:  manifest.Annotations,
					Platform:     manifest.Platform,
					ArtifactType: manifest.ArtifactType,
				},
			})
		}

		dig, err := idx.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get index digest: %w", err)
		}

		ref := repo.Digest(dig.String())

		if err := remote.WriteIndex(repo.Digest(dig.String()), idx, a.ropts...); err != nil {
			return nil, fmt.Errorf("failed to push index: %w", err)
		}

		return ref, nil

	} else if desc.MediaType.IsImage() {
		baseimg, err := remote.Image(a.base, a.ropts...)
		if err != nil {
			return nil, fmt.Errorf("failed to get image: %w", err)
		}

		mutated := baseimg
		for _, l := range layers {
			layer, err := l.Layer()
			if err != nil {
				return nil, err
			}

			mutated, err = mutate.AppendLayers(mutated, layer)
			if err != nil {
				return nil, err
			}
		}

		mdig, err := mutated.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get digest: %w", err)
		}

		ref := repo.Digest(mdig.String())
		if err := remote.Write(ref, mutated, a.ropts...); err != nil {
			return nil, fmt.Errorf("failed to push image: %w", err)
		}

		return ref, nil
	}

	return nil, fmt.Errorf("unsupported media type: %s", desc.MediaType)
}

func AppenderWithRemoteOptions(opts ...remote.Option) AppenderOpt {
	return func(a *appender) error {
		a.ropts = append(a.ropts, opts...)
		return nil
	}
}

func AppenderWithEnvs(envs ...string) AppenderOpt {
	return func(a *appender) error {
		if envs == nil {
			a.envs = make([]string, 0)
		}
		a.envs = append(a.envs, envs...)
		return nil
	}
}
