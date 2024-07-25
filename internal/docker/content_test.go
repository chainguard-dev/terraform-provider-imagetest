package docker

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContent(t *testing.T) {
	content := "Hello World"

	c := NewContentFromString(content, "/test.txt")

	checkContent(t, c, "/test.txt", content)
}

func TestContentFromFile(t *testing.T) {
	f, err := os.CreateTemp("", "test")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	_, err = f.WriteString("Hello World")
	require.NoError(t, err)

	_, err = f.Seek(0, 0)
	require.NoError(t, err)

	c, err := NewContentFromFile(f, "/test.txt")
	require.NoError(t, err)

	checkContent(t, c, "/test.txt", "Hello World")
}

func checkContent(t *testing.T, c *Content, wantTarget string, wantContent string) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, c)
	require.NoError(t, err)
	tr := tar.NewReader(&buf)

	// Check for directory entries
	dirs := strings.Split(path.Dir(wantTarget), "/")
	for i, dir := range dirs {
		if dir == "" {
			continue
		}
		hdr, err := tr.Next()
		require.NoError(t, err)
		expectedPath := "/" + path.Join(dirs[:i+1]...) + "/"
		require.Equal(t, expectedPath, hdr.Name)
		require.Equal(t, tar.TypeDir, hdr.Typeflag)
	}

	// Check for file entry
	hdr, err := tr.Next()
	require.NoError(t, err)
	require.Equal(t, wantTarget, hdr.Name)
	require.Equal(t, int64(len(wantContent)), hdr.Size)

	content, err := io.ReadAll(tr)
	require.NoError(t, err)
	require.Equal(t, wantContent, string(content))

	// Ensure there are no more entries
	_, err = tr.Next()
	require.Equal(t, io.EOF, err)
}
