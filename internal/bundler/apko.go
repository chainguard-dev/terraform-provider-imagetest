package bundler

import (
	"context"
	"fmt"
	"runtime"

	apko_build "chainguard.dev/apko/pkg/build"
	apko_oci "chainguard.dev/apko/pkg/build/oci"
	apko_types "chainguard.dev/apko/pkg/build/types"
	"chainguard.dev/apko/pkg/tarfs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// apko is a bundler that uses the local machine to build the
// image using apko.
type apko struct {
	arch       apko_types.Architecture
	apkoConfig apko_types.ImageConfiguration
	ropts      []remote.Option
}

type ApkoOpt func(*apko) error

func NewApko(opts ...ApkoOpt) (Bundler, error) {
	gid := uint32(65532)

	a := &apko{
		arch: apko_types.ParseArchitecture(runtime.GOARCH),
		apkoConfig: apko_types.ImageConfiguration{
			Contents: apko_types.ImageContents{
				Repositories: []string{
					"https://packages.wolfi.dev/os",
					"https://packages.cgr.dev/extras",
				},
				Keyring: []string{
					"https://packages.wolfi.dev/os/wolfi-signing.rsa.pub",
					"https://packages.cgr.dev/extras/chainguard-extras.rsa.pub",
				},
				Packages: []string{
					"wolfi-baselayout",
					"busybox",
					"bash",
					"apk-tools",
					"git",
					"curl",
					"jq",
				},
			},
			Cmd: "/bin/sh -l",
			Accounts: apko_types.ImageAccounts{
				RunAs: "0",
				Users: []apko_types.User{
					{
						UserName: "nonroot",
						UID:      65532,
						GID:      apko_types.GID(&gid),
					},
				},
				Groups: []apko_types.Group{
					{
						GroupName: "nonroot",
						GID:       65532,
					},
				},
			},
		},
	}

	for _, opt := range opts {
		if err := opt(a); err != nil {
			return nil, err
		}
	}

	return a, nil
}

func (a *apko) Bundle(ctx context.Context, repo name.Repository, layers ...v1.Layer) (name.Reference, error) {
	bopts := []apko_build.Option{
		apko_build.WithImageConfiguration(a.apkoConfig),
		apko_build.WithArch(a.arch),
		// apko will set the cache dir, so don't set it here
	}

	bc, err := apko_build.New(ctx, tarfs.New(), bopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}

	bde, err := bc.GetBuildDateEpoch()
	if err != nil {
		return nil, fmt.Errorf("failed to get build date epoch: %w", err)
	}

	_, layer, err := bc.BuildLayer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build layer: %w", err)
	}

	base, err := apko_oci.BuildImageFromLayer(ctx, empty.Image, layer, bc.ImageConfiguration(), bde, a.arch)
	if err != nil {
		return nil, fmt.Errorf("failed to build image: %w", err)
	}

	img, err := mutate.AppendLayers(base, layers...)
	if err != nil {
		return nil, fmt.Errorf("failed to append layers: %w", err)
	}

	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get digest: %w", err)
	}

	ref := repo.Digest(digest.String())

	if err := remote.Push(ref, img, a.ropts...); err != nil {
		return nil, fmt.Errorf("failed to push bundle: %w", err)
	}

	return ref, nil
}

func ApkoWithPackages(packages ...string) ApkoOpt {
	return func(a *apko) error {
		a.apkoConfig.Contents.Packages = append(a.apkoConfig.Contents.Packages, packages...)
		return nil
	}
}

func ApkoWithRepositories(repositories ...string) ApkoOpt {
	return func(a *apko) error {
		a.apkoConfig.Contents.Repositories = append(a.apkoConfig.Contents.Repositories, repositories...)
		return nil
	}
}

func ApkoWithKeyrings(keyrings ...string) ApkoOpt {
	return func(a *apko) error {
		a.apkoConfig.Contents.Keyring = append(a.apkoConfig.Contents.Keyring, keyrings...)
		return nil
	}
}

func ApkoWithRemoteOptions(opts ...remote.Option) ApkoOpt {
	return func(a *apko) error {
		a.ropts = append(a.ropts, opts...)
		return nil
	}
}

func ApkoWithArch(arch string) ApkoOpt {
	return func(a *apko) error {
		a.arch = apko_types.ParseArchitecture(arch)
		return nil
	}
}
