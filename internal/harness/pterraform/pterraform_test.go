package pterraform

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPterraform(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		envVars     map[string]string
		want        Connection
		expectError bool
	}{
		{
			name: "Test with an IMAGETEST_TF_VAR_ variable",
			content: fmt.Sprintf(`
variable "foo" {}

resource "terraform_data" "foo" {
    provisioner "local-exec" {
      command = "docker run -d --name ${var.foo} cgr.dev/chainguard/wolfi-base:latest tail -f /dev/null"
      when = "create"
    }
}

resource "terraform_data" "foo-down" {
    provisioner "local-exec" {
      command = "docker rm -f %s"
      when = "destroy"
    }
}

output "connection" {
  value = {
    docker = {
      cid = var.foo
    }
  }
}
      `, "foo"),
			envVars: map[string]string{
				"IMAGETEST_TF_VAR_foo": "foo",
			},
		},
		{
			name: "Ensure TF_VAR_ variables are ignored",
			content: fmt.Sprintf(`
variable "foo" {
  default = "foo"
}

resource "terraform_data" "foo" {
    provisioner "local-exec" {
      command = "docker run -d --name ${var.foo} cgr.dev/chainguard/wolfi-base:latest tail -f /dev/null"
      when = "create"
    }
}

resource "terraform_data" "foo-down" {
    provisioner "local-exec" {
      command = "docker rm -f %s"
      when = "destroy"
    }
}

output "connection" {
  value = {
    docker = {
      cid = var.foo
    }
  }
}
      `, "foo"),
			envVars: map[string]string{
				"TF_VAR_foo": "bar",
			},
		},
		{
			name: "Ensure TF_VAR_ don't pollute IMAGETEST_TF_VAR_ variables",
			content: fmt.Sprintf(`
variable "foo" {}

resource "terraform_data" "foo" {
    provisioner "local-exec" {
      command = "docker run -d --name ${var.foo} cgr.dev/chainguard/wolfi-base:latest tail -f /dev/null"
      when = "create"
    }
}

resource "terraform_data" "foo-down" {
    provisioner "local-exec" {
      command = "docker rm -f %s"
      when = "destroy"
    }
}

output "connection" {
  value = {
    docker = {
      cid = var.foo
    }
  }
}
      `, "foo"),
			envVars: map[string]string{
				"IMAGETEST_TF_VAR_foo": "foo",
				"TF_VAR_foo":           "bar",
				"TF_LOG_PROVIDER":      "info",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			// Set environment variables
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			// Clean up environment variables after the test
			defer func() {
				for key := range tt.envVars {
					_ = os.Unsetenv(key)
				}
			}()

			p, err := New(ctx, sourceFs(t, tt.content))
			if err != nil {
				t.Fatalf("unexpected error creating new pterraform: %v", err)
			}

			err = p.Create(ctx)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error: %v, got: %v", tt.expectError, err)
			}

			err = p.Destroy(ctx)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error: %v, got: %v", tt.expectError, err)
			}
		})
	}
}

func sourceFs(t *testing.T, content string) fs.FS {
	dir := t.TempDir()

	t.Logf("using temp dir %s", dir)

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return os.DirFS(dir)
}

// TestPterraformWorkdirCleanup checks that the temp working directory never
// outlives the harness: a harness that is constructed but never created (the
// provider discards skipped harnesses that way) must not leave one behind,
// and Destroy must remove it after Create, even a failed Create.
func TestPterraformWorkdirCleanup(t *testing.T) {
	tests := []struct {
		name          string
		create        bool
		wantCreateErr bool
	}{{
		name: "harness never created leaves no workdir",
	}, {
		// No "connection" output, so Create fails after the apply, once
		// the working directory holds terraform state.
		name:          "destroy after failed create removes the workdir",
		create:        true,
		wantCreateErr: true,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.create {
				if _, err := exec.LookPath("terraform"); err != nil {
					t.Skipf("terraform not on $PATH: %v", err)
				}
			}
			src := sourceFs(t, `output "unrelated" { value = "x" }`)
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx := t.Context()

			p, err := New(ctx, src)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if tc.create {
				err := p.Create(ctx)
				t.Logf("Create: err=%v", err)
				if (err != nil) != tc.wantCreateErr {
					t.Fatalf("Create: got err=%v, want error=%t", err, tc.wantCreateErr)
				}
				if err := p.Destroy(ctx); err != nil {
					t.Errorf("Destroy: %v", err)
				}
			}

			entries, err := os.ReadDir(tmp)
			if err != nil {
				t.Fatalf("reading TMPDIR %s: %v", tmp, err)
			}
			var left []string
			for _, e := range entries {
				left = append(left, e.Name())
			}
			if len(left) > 0 {
				t.Errorf("create=%t: TMPDIR has leftover entries %v, want none", tc.create, left)
			}
		})
	}
}
