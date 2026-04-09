package bundler

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const maxLayerBytes = 10 << 20 // 10 MiB

// layerWriter accumulates tar entries and produces a v1.Layer.
type layerWriter struct {
	buf   bytes.Buffer
	tw    *tar.Writer
	total int64
}

func (lw *layerWriter) writeDir(name string, fi os.FileInfo) error {
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	return lw.tw.WriteHeader(hdr)
}

func (lw *layerWriter) writeFile(name string, fi os.FileInfo, data []byte) error {
	lw.total += int64(len(data))
	if lw.total > maxLayerBytes {
		return fmt.Errorf("layer content exceeds maximum size (%d bytes)", maxLayerBytes)
	}

	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Size = int64(len(data))

	if err := lw.tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = lw.tw.Write(data)
	return err
}

func (lw *layerWriter) finish() (v1.Layer, error) {
	if err := lw.tw.Close(); err != nil {
		return nil, err
	}
	tarData := lw.buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(tarData)), nil
	})
}

// NewLayerFromFS snapshots the source filesystem into a tar layer. The content
// is buffered eagerly so the layer is stable across repeated reads.
func NewLayerFromFS(source fs.FS, target string) (v1.Layer, error) {
	var lw layerWriter
	lw.tw = tar.NewWriter(&lw.buf)

	if err := fs.WalkDir(source, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		fi, err := d.Info()
		if err != nil {
			return err
		}

		name := path.Join(target, p)

		if d.IsDir() {
			return lw.writeDir(name, fi)
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

		return lw.writeFile(name, fi, data)
	}); err != nil {
		return nil, err
	}

	return lw.finish()
}

// NewLayerFromPath creates a v1.Layer from a local path following symlinks.
func NewLayerFromPath(source string, target string) (v1.Layer, error) {
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(source)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		return newLayerFromDir(source, target)
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}

	var lw layerWriter
	lw.tw = tar.NewWriter(&lw.buf)

	if err := lw.writeFile(path.Join(target, fi.Name()), fi, data); err != nil {
		return nil, err
	}
	return lw.finish()
}

// newLayerFromDir walks root following symlinks and produces a tar layer.
// Resolved canonical paths are tracked to detect and skip symlink cycles.
func newLayerFromDir(root string, target string) (v1.Layer, error) {
	var lw layerWriter
	lw.tw = tar.NewWriter(&lw.buf)

	// Track resolved directory paths to break cycles.
	visited := map[string]bool{root: true}

	// Write a root directory entry to match NewLayerFromFS behavior.
	if ri, err := os.Stat(root); err != nil {
		return nil, err
	} else if err := lw.writeDir(target, ri); err != nil {
		return nil, err
	}

	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}

		for _, de := range entries {
			name := de.Name()
			full := filepath.Join(dir, name)
			erel := path.Join(rel, name)

			// Resolve symlinks to their real path.
			resolved := full
			if de.Type()&fs.ModeSymlink != 0 {
				resolved, err = filepath.EvalSymlinks(full)
				if err != nil {
					return err
				}
			}

			fi, err := os.Stat(resolved)
			if err != nil {
				return err
			}

			tarName := path.Join(target, erel)

			if fi.IsDir() {
				if visited[resolved] {
					continue
				}
				visited[resolved] = true

				if err := lw.writeDir(tarName, fi); err != nil {
					return err
				}
				if err := walk(resolved, erel); err != nil {
					return err
				}
				continue
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return err
			}
			if err := lw.writeFile(tarName, fi, data); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(root, "."); err != nil {
		return nil, err
	}

	return lw.finish()
}
