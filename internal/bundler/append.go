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

type AppendOpts struct {
	RemoteOptions []remote.Option
	Layers        []Layerer
	Envs          map[string]string
}

func Append(ctx context.Context, source name.Reference, target name.Repository, opts AppendOpts) (name.Reference, error) {
	desc, err := remote.Get(source, opts.RemoteOptions...)
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

			mutated, err := appendLayers(baseimg, opts.Layers...)
			if err != nil {
				return nil, err
			}

			mutated, err = mutateConfig(mutated, opts.Envs)
			if err != nil {
				return nil, err
			}

			mdig, err := mutated.Digest()
			if err != nil {
				return nil, fmt.Errorf("failed to get digest: %w", err)
			}

			if err := remote.Write(target.Digest(mdig.String()), mutated, opts.RemoteOptions...); err != nil {
				return nil, fmt.Errorf("failed to push image: %w", err)
			}

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

		ref := target.Digest(dig.String())
		if err := remote.WriteIndex(ref, idx, opts.RemoteOptions...); err != nil {
			return nil, fmt.Errorf("failed to push index: %w", err)
		}

		return ref, nil

	} else if desc.MediaType.IsImage() {
		baseimg, err := remote.Image(source, opts.RemoteOptions...)
		if err != nil {
			return nil, fmt.Errorf("failed to get image: %w", err)
		}

		mutated, err := appendLayers(baseimg, opts.Layers...)
		if err != nil {
			return nil, err
		}

		mutated, err = mutateConfig(mutated, opts.Envs)
		if err != nil {
			return nil, err
		}

		mdig, err := mutated.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get digest: %w", err)
		}

		ref := target.Digest(mdig.String())
		if err := remote.Write(ref, mutated, opts.RemoteOptions...); err != nil {
			return nil, fmt.Errorf("failed to push image: %w", err)
		}

		return ref, nil
	}

	return nil, fmt.Errorf("unsupported media type: %s", desc.MediaType)
}

func mutateConfig(img v1.Image, envs map[string]string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get config file: %w", err)
	}

	for k, v := range envs {
		cfg.Config.Env = append(cfg.Config.Env, fmt.Sprintf("%s=%s", k, v))
	}

	return mutate.ConfigFile(img, cfg)
}
