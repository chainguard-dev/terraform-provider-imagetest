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
	Layers        []v1.Layer
	Envs          map[string]string
	Cmd           string
	Entrypoint    []string
}

type MutateOpts struct {
	RemoteOptions []remote.Option
	ImageMutators []func(base v1.Image) (v1.Image, error)
}

func Mutate(ctx context.Context, base name.Reference, target name.Repository, opts MutateOpts) (name.Reference, error) {
	desc, err := remote.Get(base, opts.RemoteOptions...)
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

			if err := remote.Write(target.Digest(dig.String()), img, opts.RemoteOptions...); err != nil {
				return nil, fmt.Errorf("failed to push image: %w", err)
			}

			midx = mutate.AppendManifests(midx, mutate.IndexAddendum{
				Add: img,
				Descriptor: v1.Descriptor{
					MediaType:    m.MediaType,
					URLs:         m.URLs,
					Annotations:  m.Annotations,
					Platform:     m.Platform,
					ArtifactType: m.ArtifactType,
				},
			})
		}

		dig, err := midx.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get index digest: %w", err)
		}

		ref := target.Digest(dig.String())
		if err := remote.WriteIndex(ref, midx, opts.RemoteOptions...); err != nil {
			return nil, fmt.Errorf("failed to push index: %w", err)
		}

		return ref, nil

	} else if desc.MediaType.IsImage() {
		img, err := remote.Image(base, opts.RemoteOptions...)
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
		if err := remote.Write(ref, img, opts.RemoteOptions...); err != nil {
			return nil, fmt.Errorf("failed to push image: %w", err)
		}

		return ref, nil
	}

	return nil, fmt.Errorf("reference [%s] uses an unsupported media type: [%s]", base.String(), desc.MediaType)
}

// Append mutates the source Image or ImageIndex with the provided append
// options, and pushes it to the target repository via its digest.
func Append(ctx context.Context, base name.Reference, target name.Repository, opts AppendOpts) (name.Reference, error) {
	desc, err := remote.Get(base, opts.RemoteOptions...)
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

			mutated, err := appendToImage(ctx, baseimg, opts)
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
		baseimg, err := remote.Image(base, opts.RemoteOptions...)
		if err != nil {
			return nil, fmt.Errorf("failed to get image: %w", err)
		}

		mutated, err := appendToImage(ctx, baseimg, opts)
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

func appendToImage(_ context.Context, img v1.Image, opts AppendOpts) (v1.Image, error) {
	mutated, err := mutate.AppendLayers(img, opts.Layers...)
	if err != nil {
		return nil, err
	}

	mutated, err = mutateConfig(mutated, opts.Envs, opts.Entrypoint, opts.Cmd)
	if err != nil {
		return nil, err
	}

	return mutated, nil
}

func mutateConfig(img v1.Image, envs map[string]string, entrypoint []string, cmd string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get config file: %w", err)
	}

	for k, v := range envs {
		cfg.Config.Env = append(cfg.Config.Env, fmt.Sprintf("%s=%s", k, v))
	}

	if cmd != "" {
		cfg.Config.Cmd = []string{cmd}
	}

	if len(entrypoint) > 0 {
		cfg.Config.Entrypoint = entrypoint
	}

	return mutate.ConfigFile(img, cfg)
}
