package bundler

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path"

	"chainguard.dev/apko/pkg/tarfs"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

type Layerer interface {
	Layer() (v1.Layer, error)
}

var _ Layerer = &fsl{}

type fsl struct {
	source fs.FS
	target string
}

func NewFSLayer(source fs.FS, target string) Layerer {
	return &fsl{
		source: source,
		target: target,
	}
}

// NewFSLayerFromPath is a helper function that creates a FS from a local path.
func NewFSLayerFromPath(source string, target string) Layerer {
	pi, err := os.Stat(source)
	if err != nil {
		return nil
	}

	if pi.IsDir() {
		return NewFSLayer(os.DirFS(source), target)
	}

	// There are better ways to make an FS from a isngle layer, but we already
	// import tfs through apko, so just be a little lazy here
	data, err := os.ReadFile(source)
	if err != nil {
		return nil
	}

	tfs := tarfs.New()
	if err := tfs.WriteFile(pi.Name(), data, pi.Mode()); err != nil {
		return nil
	}

	return NewFSLayer(tfs, target)
}

func (l *fsl) Layer() (v1.Layer, error) {
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		pr, pw := io.Pipe()

		go func() {
			tw := tar.NewWriter(pw)
			defer tw.Close()
			defer pw.Close()

			if err := fs.WalkDir(l.source, ".", func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				fi, err := d.Info()
				if err != nil {
					return err
				}

				hdr, err := tar.FileInfoHeader(fi, "")
				if err != nil {
					return err
				}

				hdr.Name = path.Join(l.target, p)

				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}

				if !d.IsDir() {
					f, err := l.source.Open(p)
					if err != nil {
						return err
					}
					defer f.Close()

					if _, err := io.Copy(tw, f); err != nil {
						return err
					}
				}

				return nil
			}); err != nil {
				pw.CloseWithError(err)
				return
			}
		}()

		return pr, nil
	})
}

func appendLayers(img v1.Image, layers ...Layerer) (v1.Image, error) {
	mutated := img

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
	return mutated, nil
}
