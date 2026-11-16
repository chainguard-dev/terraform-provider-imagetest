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

type MutateOpts struct {
	RemoteOptions []remote.Option
	ImageMutators []func(base v1.Image) (v1.Image, error)
}

func Mutate(ctx context.Context, base name.Reference, target name.Repository, opts MutateOpts) (name.Reference, error) {
	ropts := append(opts.RemoteOptions, remote.WithContext(ctx))

	desc, err := remote.Get(base, ropts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	if desc.MediaType.IsIndex() {
		baseidx, err := desc.ImageIndex()
		if err != nil {
			return nil, fmt.Errorf("failed to get image index: %w", err)
		}

		var midx v1.ImageIndex = empty.Index

		mfst, err := baseidx.IndexManifest()
		if err != nil {
			return nil, fmt.Errorf("failed to get index manifest: %w", err)
		}

		for _, m := range mfst.Manifests {
			img, err := baseidx.Image(m.Digest)
			if err != nil {
				return nil, fmt.Errorf("failed to load image: %w", err)
			}

			for _, mutator := range opts.ImageMutators {
				img, err = mutator(img)
				if err != nil {
					return nil, fmt.Errorf("failed to mutate image: %w", err)
				}
			}

			dig, err := img.Digest()
			if err != nil {
				return nil, fmt.Errorf("failed to get digest: %w", err)
			}

			if err := remote.Write(target.Digest(dig.String()), img, ropts...); err != nil {
				return nil, fmt.Errorf("failed to push image: %w", err)
			}

			midx = mutate.AppendManifests(midx, mutate.IndexAddendum{
				Add:          img,
				MediaType:    m.MediaType,
				URLs:         m.URLs,
				Annotations:  m.Annotations,
				Platform:     m.Platform,
				ArtifactType: m.ArtifactType,
			})
		}

		dig, err := midx.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get index digest: %w", err)
		}

		ref := target.Digest(dig.String())
		if err := remote.WriteIndex(ref, midx, ropts...); err != nil {
			return nil, fmt.Errorf("failed to push index: %w", err)
		}

		return ref, nil

	} else if desc.MediaType.IsImage() {
		img, err := remote.Image(base, ropts...)
		if err != nil {
			return nil, fmt.Errorf("failed to get image: %w", err)
		}

		for _, mutator := range opts.ImageMutators {
			img, err = mutator(img)
			if err != nil {
				return nil, fmt.Errorf("failed to mutate image: %w", err)
			}
		}

		mdig, err := img.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get digest: %w", err)
		}

		ref := target.Digest(mdig.String())
		if err := remote.Write(ref, img, ropts...); err != nil {
			return nil, fmt.Errorf("failed to push image: %w", err)
		}

		return ref, nil
	}

	return nil, fmt.Errorf("reference [%s] uses an unsupported media type: [%s]", base.String(), desc.MediaType)
}
