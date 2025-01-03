package pterraform

import (
	"context"
	"fmt"
	"io/fs"
	"os"
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
			ctx := context.Background()

			// Set environment variables
			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			// Clean up environment variables after the test
			defer func() {
				for key := range tt.envVars {
					os.Unsetenv(key)
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

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	return os.DirFS(dir)
}
