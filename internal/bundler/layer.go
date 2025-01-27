package bundler

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path"

	"chainguard.dev/apko/pkg/tarfs"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// NewLayerFromFS creates a v1.Layer from a filesystem and target path.
func NewLayerFromFS(source fs.FS, target string) (v1.Layer, error) {
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		pr, pw := io.Pipe()

		go func() {
			tw := tar.NewWriter(pw)
			defer tw.Close()
			defer pw.Close()

			if err := fs.WalkDir(source, ".", func(p string, d fs.DirEntry, err error) error {
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

				hdr.Name = path.Join(target, p)

				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}

				if !d.IsDir() {
					f, err := source.Open(p)
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

// NewLayerFromPath creates a v1.Layer from a local path.
func NewLayerFromPath(source string, target string) (v1.Layer, error) {
	pi, err := os.Stat(source)
	if err != nil {
		return nil, err
	}

	if pi.IsDir() {
		return NewLayerFromFS(os.DirFS(source), target)
	}

	// Handle single file
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}

	tfs := tarfs.New()
	if err := tfs.WriteFile(pi.Name(), data, pi.Mode()); err != nil {
		return nil, err
	}

	return NewLayerFromFS(tfs, target)
}
