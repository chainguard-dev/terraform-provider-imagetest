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

// appender is a bundler that appends layers to existing images,
// copying the base to the target repo.
type appender struct {
	base  name.Reference
	ropts []remote.Option
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
