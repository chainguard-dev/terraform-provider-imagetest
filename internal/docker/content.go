package docker

import (
	"archive/tar"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var _ io.Reader = &Content{}

type Content struct {
	Target string
	Dir    string

	reader io.Reader
	size   int64

	pw *io.PipeWriter
	pr *io.PipeReader
}

// Read implements io.Reader.
func (c Content) Read(p []byte) (n int, err error) {
	return c.pr.Read(p)
}

func NewContentFromFile(f *os.File, target string) (*Content, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, err
	}

	return NewContent(f, target, info.Size()), nil
}

func NewContent(r io.Reader, target string, size int64) *Content {
	pr, pw := io.Pipe()

	// Normalize the target path
	cleanTarget := path.Clean("/" + filepath.ToSlash(target))

	content := &Content{
		Target: cleanTarget,
		Dir:    path.Dir(cleanTarget),
		reader: r,
		size:   size,
		pw:     pw,
		pr:     pr,
	}

	go content.stream()
	return content
}

func NewContentFromString(s string, target string) *Content {
	return NewContent(strings.NewReader(s), target, int64(len(s)))
}

func (c *Content) stream() {
	tw := tar.NewWriter(c.pw)
	defer tw.Close()
	defer c.pw.Close()

	dirs := strings.Split(strings.Trim(c.Dir, "/"), "/")
	currentPath := ""
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		currentPath = path.Join(currentPath, dir)
		hdr := &tar.Header{
			Name:     path.Join("/", currentPath) + "/",
			Mode:     0755,
			Typeflag: tar.TypeDir,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			c.pw.CloseWithError(err)
			return
		}
	}

	hdr := &tar.Header{
		Name:     c.Target,
		Mode:     0644,
		Size:     c.size,
		Typeflag: tar.TypeReg,
	}

	if err := tw.WriteHeader(hdr); err != nil {
		c.pw.CloseWithError(err)
		return
	}

	if _, err := io.Copy(tw, c.reader); err != nil {
		c.pw.CloseWithError(err)
		return
	}
}
