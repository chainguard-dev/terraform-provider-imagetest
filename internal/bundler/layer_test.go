package bundler

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

type tarEntry struct {
	Content string
	Mode    int64 // 0 means don't check
}

func checkLayer(t *testing.T, l v1.Layer, want map[string]tarEntry) {
	t.Helper()
	rc, err := l.Uncompressed()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	got := make(map[string]tarEntry)
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = tarEntry{Content: string(data), Mode: hdr.Mode}
	}

	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("missing entry %q", name)
			continue
		}
		if diff := cmp.Diff(w.Content, g.Content); diff != "" {
			t.Errorf("entry %q content (-want +got):\n%s", name, diff)
		}
		if w.Mode != 0 && g.Mode != w.Mode {
			t.Errorf("entry %q mode: got %o, want %o", name, g.Mode, w.Mode)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected entry %q", name)
		}
	}
}

func TestNewLayerFromFS(t *testing.T) {
	tests := []struct {
		name    string
		fs      fs.FS
		target  string
		want    map[string]tarEntry
		wantErr string
	}{
		{
			name: "flat directory",
			fs: fstest.MapFS{
				"a.txt": {Data: []byte("alpha")},
				"b.txt": {Data: []byte("bravo")},
			},
			target: "/dest",
			want: map[string]tarEntry{
				"/dest/a.txt": {Content: "alpha"},
				"/dest/b.txt": {Content: "bravo"},
			},
		},
		{
			name: "nested subdirectories",
			fs: fstest.MapFS{
				"top.sh":        {Data: []byte("#!/bin/sh\n")},
				"sub/inner.sh":  {Data: []byte("inner\n")},
				"sub/deep/f.sh": {Data: []byte("deep\n")},
			},
			target: "/app",
			want: map[string]tarEntry{
				"/app/top.sh":        {Content: "#!/bin/sh\n"},
				"/app/sub/inner.sh":  {Content: "inner\n"},
				"/app/sub/deep/f.sh": {Content: "deep\n"},
			},
		},
		{
			name:   "empty filesystem",
			fs:     fstest.MapFS{},
			target: "/empty",
			want:   map[string]tarEntry{},
		},
		{
			name: "empty file",
			fs: fstest.MapFS{
				"empty.txt": {Data: []byte{}},
			},
			target: "/dest",
			want: map[string]tarEntry{
				"/dest/empty.txt": {Content: ""},
			},
		},
		{
			name: "executable permission preserved",
			fs: fstest.MapFS{
				"run.sh": {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
			},
			target: "/bin",
			want: map[string]tarEntry{
				"/bin/run.sh": {Content: "#!/bin/sh\n", Mode: 0o755},
			},
		},
		{
			name: "size limit exceeded by single file",
			fs: fstest.MapFS{
				"big.bin": {Data: make([]byte, maxLayerBytes+1)},
			},
			target:  "/dst",
			wantErr: "layer content exceeds maximum size",
		},
		{
			name: "size limit exceeded cumulatively",
			fs: fstest.MapFS{
				"a.bin": {Data: make([]byte, maxLayerBytes/2+1)},
				"b.bin": {Data: make([]byte, maxLayerBytes/2+1)},
			},
			target:  "/dst",
			wantErr: "layer content exceeds maximum size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layer, err := NewLayerFromFS(tt.fs, tt.target)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			checkLayer(t, layer, tt.want)
		})
	}
}

func TestNewLayerFromFS_RepeatedReads(t *testing.T) {
	src := fstest.MapFS{
		"f.txt": {Data: []byte("stable")},
	}

	layer, err := NewLayerFromFS(src, "/rep")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]tarEntry{"/rep/f.txt": {Content: "stable"}}
	checkLayer(t, layer, want)
	checkLayer(t, layer, want)
}

func TestNewLayerFromPath(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) string
		target  string
		want    map[string]tarEntry
		wantErr string
	}{
		{
			name: "single file",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				p := filepath.Join(dir, "hello.sh")
				if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return p
			},
			target: "/scripts",
			want: map[string]tarEntry{
				"/scripts/hello.sh": {Content: "hello\n"},
			},
		},
		{
			name: "directory with nested files",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				sub := filepath.Join(dir, "sub")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			target: "/content",
			want: map[string]tarEntry{
				"/content/a.txt":     {Content: "a"},
				"/content/sub/b.txt": {Content: "b"},
			},
		},
		{
			name: "executable permission preserved",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				p := filepath.Join(dir, "run.sh")
				if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			target: "/bin",
			want: map[string]tarEntry{
				"/bin/run.sh": {Content: "#!/bin/sh\n", Mode: 0o755},
			},
		},
		{
			name: "symlink to external file followed",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				outside := filepath.Join(t.TempDir(), "data.txt")
				if err := os.WriteFile(outside, []byte("external"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			target: "/dst",
			want: map[string]tarEntry{
				"/dst/link.txt": {Content: "external"},
			},
		},
		{
			name: "symlink to external directory followed",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "a.txt"), []byte("aaa"), 0o644); err != nil {
					t.Fatal(err)
				}
				sub := filepath.Join(outside, "sub")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("bbb"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, "ext")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			target: "/dst",
			want: map[string]tarEntry{
				"/dst/ext/a.txt":     {Content: "aaa"},
				"/dst/ext/sub/b.txt": {Content: "bbb"},
			},
		},
		{
			name: "symlink cycle skipped",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("ok"), 0o644); err != nil {
					t.Fatal(err)
				}
				// Create a symlink back to the directory itself.
				if err := os.Symlink(dir, filepath.Join(dir, "loop")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			target: "/dst",
			want: map[string]tarEntry{
				"/dst/real.txt": {Content: "ok"},
			},
		},
		{
			name: "nonexistent path",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nope")
			},
			target:  "/dst",
			wantErr: "no such file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := tt.setup(t)

			layer, err := NewLayerFromPath(source, tt.target)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			checkLayer(t, layer, tt.want)
		})
	}
}
