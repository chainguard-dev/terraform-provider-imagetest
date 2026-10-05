package drivers

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingReader yields data and then fails, like an artifact stream that
// drops mid-transfer.
type failingReader struct {
	data io.Reader
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	n, err := r.data.Read(p)
	if err == io.EOF {
		return n, r.err
	}
	return n, err
}

func (r *failingReader) Close() error { return nil }

// TestNewRunArtifactResultTempFile checks the lifetime of the artifact file:
// on success it is kept (its file:// URI is handed to the user in the test
// resource's state), on a failed copy no partial file is left behind, since
// its path is never reported to anyone.
func TestNewRunArtifactResultTempFile(t *testing.T) {
	const payload = "artifact bundle contents"
	tests := []struct {
		name     string
		rc       io.ReadCloser
		wantErr  bool
		wantKept bool
	}{{
		name:     "successful copy keeps the artifact for the user",
		rc:       io.NopCloser(strings.NewReader(payload)),
		wantKept: true,
	}, {
		name:    "failed copy leaves no partial artifact",
		rc:      &failingReader{data: strings.NewReader(payload), err: errors.New("stream dropped")},
		wantErr: true,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)

			got, err := NewRunArtifactResult(t.Context(), tc.rc)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewRunArtifactResult: got result=%+v err=%v, want error=%t", got, err, tc.wantErr)
			}

			entries, rerr := os.ReadDir(tmp)
			if rerr != nil {
				t.Fatalf("reading TMPDIR %s: %v", tmp, rerr)
			}
			var left []string
			for _, e := range entries {
				left = append(left, e.Name())
			}

			if !tc.wantKept {
				if len(left) > 0 {
					t.Errorf("TMPDIR has leftover entries %v, want none (err=%v)", left, err)
				}
				return
			}

			u, perr := url.Parse(got.URI)
			if perr != nil {
				t.Fatalf("parsing artifact URI %q: %v", got.URI, perr)
			}
			if len(left) != 1 || filepath.Join(tmp, left[0]) != u.Path {
				t.Fatalf("TMPDIR entries = %v, want exactly the artifact at %s", left, u.Path)
			}
			data, rerr := os.ReadFile(u.Path)
			if rerr != nil {
				t.Fatalf("reading kept artifact %s: %v", u.Path, rerr)
			}
			if string(data) != payload {
				t.Errorf("artifact contents = %q, want %q", data, payload)
			}
		})
	}
}
