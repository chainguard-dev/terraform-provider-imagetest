package bundler

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"chainguard.dev/apko/pkg/tarfs"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const maxLayerBytes = 10 << 20 // 10 MiB

// NewLayerFromFS snapshots the source filesystem into a tar layer. The content
// is buffered eagerly so the layer is stable across repeated reads.
func NewLayerFromFS(source fs.FS, target string) (v1.Layer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var total int64

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

		if d.IsDir() {
			return tw.WriteHeader(hdr)
		}

		total += fi.Size()
		if total > maxLayerBytes {
			return fmt.Errorf("layer content exceeds maximum size (%d bytes)", maxLayerBytes)
		}

		f, err := source.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()

		data, err := io.ReadAll(f)
		if err != nil {
			return err
		}

		hdr.Size = int64(len(data))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	}); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	tarData := buf.Bytes()

	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(tarData)), nil
	})
}

// NewLayerFromPath creates a v1.Layer from a local path. For directories,
// os.OpenRoot is used to prevent symlink escapes during the walk.
func NewLayerFromPath(source string, target string) (v1.Layer, error) {
	pi, err := os.Stat(source)
	if err != nil {
		return nil, err
	}

	if pi.IsDir() {
		root, err := os.OpenRoot(source)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		return NewLayerFromFS(root.FS(), target)
	}

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
