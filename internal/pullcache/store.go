package pullcache

import (
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// store is a content addressed on-disk cache. Blobs and manifests are keyed
// by digest, so entries are immutable once written and safe to share between
// any number of concurrent readers.
type store struct {
	dir string
}

func newStore(dir string) (*store, error) {
	for _, sub := range []string{"blobs", "manifests", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("creating cache dir: %w", err)
		}
	}
	return &store{dir: dir}, nil
}

func (s *store) blobPath(h v1.Hash) string {
	return filepath.Join(s.dir, "blobs", h.Algorithm, h.Hex)
}

func (s *store) manifestPath(h v1.Hash) string {
	return filepath.Join(s.dir, "manifests", h.Algorithm, h.Hex)
}

// openBlob returns an open file for the blob if it is cached.
func (s *store) openBlob(h v1.Hash) (*os.File, bool) {
	f, err := os.Open(s.blobPath(h))
	if err != nil {
		return nil, false
	}
	return f, true
}

// putBlob streams r into the cache, verifying that its content matches h. The
// write goes through a temp file and an atomic rename, so partial downloads are
// never observable.
func (s *store) putBlob(h v1.Hash, r io.Reader) (int64, error) {
	hasher, err := newHasher(h.Algorithm)
	if err != nil {
		return 0, err
	}

	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), h.Hex+"-*")
	if err != nil {
		return 0, fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	n, err := io.Copy(io.MultiWriter(tmp, hasher), r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("writing blob: %w", err)
	}

	if got := fmt.Sprintf("%x", hasher.Sum(nil)); got != h.Hex {
		return 0, fmt.Errorf("digest mismatch for %s: got %s:%s", h, h.Algorithm, got)
	}

	dst := s.blobPath(h)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		// Another writer may have won the race, which is fine.
		if _, serr := os.Stat(dst); serr == nil {
			return n, nil
		}
		return 0, fmt.Errorf("committing blob: %w", err)
	}
	return n, nil
}

// getManifest returns the cached manifest body and media type, if present.
func (s *store) getManifest(h v1.Hash) ([]byte, string, bool) {
	body, err := os.ReadFile(s.manifestPath(h))
	if err != nil {
		return nil, "", false
	}
	mt, err := os.ReadFile(s.manifestPath(h) + ".mediatype")
	if err != nil {
		return nil, "", false
	}
	return body, string(mt), true
}

func (s *store) putManifest(h v1.Hash, mediaType string, body []byte) error {
	dst := s.manifestPath(h)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Write the media type first so a reader never sees a body without one.
	if err := atomicWrite(filepath.Join(s.dir, "tmp"), dst+".mediatype", []byte(mediaType)); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, "tmp"), dst, body)
}

func atomicWrite(tmpdir, dst string, data []byte) error {
	tmp, err := os.CreateTemp(tmpdir, filepath.Base(dst)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		if _, serr := os.Stat(dst); serr == nil {
			return nil
		}
		return err
	}
	return nil
}

func newHasher(algorithm string) (hash.Hash, error) {
	switch algorithm {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, errors.New("unsupported digest algorithm: " + algorithm)
	}
}
