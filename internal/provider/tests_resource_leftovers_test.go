package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// fakeLocalFiler is a driver that reports host files it would remove on
// teardown.
type fakeLocalFiler struct {
	fakeTester
	files []drivers.LocalFile
}

func (f fakeLocalFiler) LocalFiles() []drivers.LocalFile { return f.files }

// TestLeftoverFiles checks the warning that tells a user, at the end of the
// run, which of a driver's host files are still on disk after the teardown
// step: kept on purpose because teardown was skipped, or left by a teardown
// that did not finish.
func TestLeftoverFiles(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present-kubeconfig")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	removed := filepath.Join(dir, "removed-kubeconfig")

	tests := []struct {
		name string
		dr   drivers.Tester
		// wantPaths are the paths the warning must name; none means no
		// warning at all.
		wantPaths []string
		// notPaths must not appear in the warning.
		notPaths []string
	}{{
		name: "driver that creates no host files reports nothing",
		dr:   fakeTester{},
	}, {
		name:      "file still on disk is reported with its description",
		dr:        fakeLocalFiler{files: []drivers.LocalFile{{Path: present, Description: "kubeconfig for cluster c1"}}},
		wantPaths: []string{"kubeconfig for cluster c1: " + present},
	}, {
		name: "file teardown removed is not reported",
		dr:   fakeLocalFiler{files: []drivers.LocalFile{{Path: removed, Description: "kubeconfig for cluster c1"}}},
	}, {
		name: "file the driver never created is not reported",
		dr:   fakeLocalFiler{files: []drivers.LocalFile{{Path: "", Description: "kubeconfig for cluster c1"}}},
	}, {
		name: "only the files still on disk are reported",
		dr: fakeLocalFiler{files: []drivers.LocalFile{
			{Path: removed, Description: "gone"},
			{Path: present, Description: "kept"},
		}},
		wantPaths: []string{"kept: " + present},
		notPaths:  []string{removed},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := leftoverFiles(tc.dr)
			if len(tc.wantPaths) == 0 {
				if d != nil {
					t.Fatalf("leftoverFiles(%T): want no diagnostic, got %s: %s", tc.dr, d.Summary(), d.Detail())
				}
				return
			}
			if d == nil {
				t.Fatalf("leftoverFiles(%T): want a warning naming %v, got none", tc.dr, tc.wantPaths)
			}
			if d.Severity() != diag.SeverityWarning {
				t.Errorf("leftoverFiles(%T): severity = %v, want warning", tc.dr, d.Severity())
			}
			for _, p := range tc.wantPaths {
				if !strings.Contains(d.Detail(), p) {
					t.Errorf("leftoverFiles(%T): detail does not name %q; got:\n%s", tc.dr, p, d.Detail())
				}
			}
			for _, p := range tc.notPaths {
				if strings.Contains(d.Detail(), p) {
					t.Errorf("leftoverFiles(%T): detail names removed file %q; got:\n%s", tc.dr, p, d.Detail())
				}
			}
		})
	}
}

// removingDriver removes its host file on Teardown, like a real driver, unless
// told to fail first.
type removingDriver struct {
	fakeTester
	path string
	fail bool
}

func (r removingDriver) Teardown(context.Context) error {
	if r.fail {
		return errors.New("cluster delete failed")
	}
	return os.Remove(r.path)
}

func (r removingDriver) LocalFiles() []drivers.LocalFile {
	return []drivers.LocalFile{{Path: r.path, Description: "kubeconfig for cluster c1"}}
}

// TestTeardownAndReport checks the end of a run: the file is reported exactly
// when the teardown step leaves it on disk, under each way teardown can be
// skipped or fail.
func TestTeardownAndReport(t *testing.T) {
	tests := []struct {
		name          string
		skip          string // IMAGETEST_SKIP_TEARDOWN
		skipOnFailure string // IMAGETEST_SKIP_TEARDOWN_ON_FAILURE
		failed        bool   // whether the test failed
		teardownFails bool
		wantTeardown  bool // a teardown diagnostic is expected
		wantReported  bool // the leftover file is expected in a warning
	}{{
		name: "teardown removes the file and nothing is reported",
	}, {
		name:         "IMAGETEST_SKIP_TEARDOWN keeps the file and reports it",
		skip:         "true",
		wantTeardown: true,
		wantReported: true,
	}, {
		name:          "IMAGETEST_SKIP_TEARDOWN_ON_FAILURE after a failure keeps the file and reports it",
		skipOnFailure: "true",
		failed:        true,
		wantTeardown:  true,
		wantReported:  true,
	}, {
		name:          "IMAGETEST_SKIP_TEARDOWN_ON_FAILURE after a pass tears down and reports nothing",
		skipOnFailure: "true",
	}, {
		name:          "a teardown that fails before removing the file reports it",
		teardownFails: true,
		wantTeardown:  true,
		wantReported:  true,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("IMAGETEST_SKIP_TEARDOWN", tc.skip)
			t.Setenv("IMAGETEST_SKIP_TEARDOWN_ON_FAILURE", tc.skipOnFailure)
			path := filepath.Join(t.TempDir(), "kubeconfig")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			dr := removingDriver{path: path, fail: tc.teardownFails}

			td, left := (&TestsResource{}).teardownAndReport(t.Context(), dr, tc.failed)

			if got := td != nil; got != tc.wantTeardown {
				t.Errorf("teardown diagnostic present=%t, want %t (diag=%v)", got, tc.wantTeardown, td)
			}
			reported := left != nil && strings.Contains(left.Detail(), path)
			if reported != tc.wantReported {
				t.Errorf("leftover %s reported=%t, want %t (skip=%q skipOnFailure=%q failed=%t teardownFails=%t, left=%v)",
					path, reported, tc.wantReported, tc.skip, tc.skipOnFailure, tc.failed, tc.teardownFails, left)
			}
		})
	}
}
