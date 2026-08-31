package provider

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarLayer builds an in-memory layer containing the given paths. Paths with a
// trailing "/" become directories, everything else an empty regular file.
func tarLayer(t *testing.T, paths ...string) v1.Layer {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, p := range paths {
		hdr := &tar.Header{Name: p, Mode: 0o644, Typeflag: tar.TypeReg}
		if p[len(p)-1] == '/' {
			hdr.Typeflag = tar.TypeDir
			hdr.Mode = 0o755
		}
		require.NoError(t, tw.WriteHeader(hdr))
	}
	require.NoError(t, tw.Close())

	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
	})
	require.NoError(t, err)
	return l
}

func TestFilterEntrypointLayers(t *testing.T) {
	// Mirrors the ko-built entrypoint image: a base image layer followed by
	// the kodata and app binary layers. Path styles (leading "/" or not)
	// match what ko and apko actually produce.
	baseLayer := tarLayer(t,
		"bin/",
		"etc/apk/repositories",
		"etc/apk/world",
		"usr/lib/apk/db/installed",
	)
	kodataLayer := tarLayer(t,
		"/var/",
		"/var/run/",
		"/var/run/ko/",
		"/var/run/ko/entrypoint-wrapper.sh",
	)
	appLayer := tarLayer(t,
		"ko-app/",
		"/ko-app/entrypoint",
	)

	t.Run("drops base image layers", func(t *testing.T) {
		got, err := filterEntrypointLayers([]v1.Layer{baseLayer, kodataLayer, appLayer})
		require.NoError(t, err)
		assert.Equal(t, []v1.Layer{kodataLayer, appLayer}, got)
	})

	t.Run("filtered layers contain no apk paths", func(t *testing.T) {
		got, err := filterEntrypointLayers([]v1.Layer{baseLayer, kodataLayer, appLayer})
		require.NoError(t, err)

		for _, l := range got {
			rc, err := l.Uncompressed()
			require.NoError(t, err)
			defer rc.Close()

			tr := tar.NewReader(rc)
			for {
				hdr, err := tr.Next()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				assert.True(t, entrypointPathAllowed(hdr.Name), "unexpected path %q in filtered layer", hdr.Name)
			}
		}
	})

	t.Run("errors when only base layers remain", func(t *testing.T) {
		_, err := filterEntrypointLayers([]v1.Layer{baseLayer})
		require.Error(t, err)
	})
}

func TestEntrypointPathAllowed(t *testing.T) {
	allowed := []string{
		"/", ".", "./",
		"ko-app", "ko-app/", "/ko-app/entrypoint", "./ko-app/entrypoint",
		"var", "/var/run", "var/run/ko/", "/var/run/ko/entrypoint-wrapper.sh",
	}
	for _, p := range allowed {
		assert.True(t, entrypointPathAllowed(p), "expected %q to be allowed", p)
	}

	denied := []string{
		"bin", "etc/apk/world", "usr/lib/apk/db/installed",
		"var/run/secrets", "var/log", "ko-app2/entrypoint",
	}
	for _, p := range denied {
		assert.False(t, entrypointPathAllowed(p), "expected %q to be denied", p)
	}
}
