package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource(t *testing.T) {
	repo := testRegistry(t, context.Background())
	t.Logf("serving ephemeral test registry at %s", repo)

	k3sindockerTpl := `
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "k3s_in_docker"

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:4559395ca443fc5d7be4dc813370ef3de4dda8561d1b9a8a24dc578027339791"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "./%s"
    }
  ]

  // Something before GHA timeouts
  timeout = "5m"
}
  `

	dockerindockerTpl := `
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "docker_in_docker"

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:4559395ca443fc5d7be4dc813370ef3de4dda8561d1b9a8a24dc578027339791"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/busybox:latest"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "./%s"
    }
  ]

  // Something before GHA timeouts
  timeout = "5m"
}
  `

	testCases := map[string][]resource.TestStep{
		"k3sindocker-basic":          {{Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-basic.sh")}},
		"k3sindocker-non-executable": {{Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-non-executable.sh")}},
		// ensure command's exit code surfaces in tf error
		"k3sindocker-fails-with-proper-exit-code": {
			{
				Config:      fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-fails-with-proper-exit-code.sh"),
				ExpectError: regexp.MustCompile(`.*213.*`),
			},
		},
		// ensures set -eux is always plumbed through and command stderr is surfaced in tf error
		"k3sindocker-fails-with-bad-command": {
			{
				Config:      fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-fails-with-bad-command.sh"),
				ExpectError: regexp.MustCompile(`.*No such file or directory.*`),
			},
		},
		// TODO: This test will leave the clusters dangling and needs to be
		// manually cleaned up.
		// "k3sindocker-enters-debug-mode-with-proper-exit-code-on-failure": {
		// 	{
		// 		Config:      fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-fails-with-proper-exit-code.sh"),
		// 		ExpectError: regexp.MustCompile(`.*213.*`),
		// 		PreConfig: func() {
		// 			if err := os.Setenv("IMAGETEST_SKIP_TEARDOWN", "true"); err != nil {
		// 				t.Fatal(err)
		// 			}
		// 		},
		// 	},
		// },
		"k3sindocker-hooks": {
			{
				Config: fmt.Sprintf(`
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "k3s_in_docker"

  drivers = {
    k3s_in_docker = {
      hooks = {
        post_start = ["kubectl run foo --image=cgr.dev/chainguard/busybox:latest --restart=Never -- tail -f /dev/null"]
      }
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:4559395ca443fc5d7be4dc813370ef3de4dda8561d1b9a8a24dc578027339791"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "./%s"
    }
  ]

  // Something before GHA timeouts
  timeout = "5m"
}
        `, "k3s-in-docker-hooks.sh"),
			},
		},

		"dockerindocker-basic": {{Config: fmt.Sprintf(dockerindockerTpl, "docker-in-docker-basic.sh")}},
		"dockerindocker-fails-message": {
			{
				Config:      fmt.Sprintf(dockerindockerTpl, "docker-in-docker-fails.sh"),
				ExpectError: regexp.MustCompile(`.*can't open 'imalittleteapot'.*`),
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.Test(t, resource.TestCase{
				PreCheck: func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
					"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
						repo: repo,
					}),
				},
				Steps: tc,
			})
		})
	}
}

func TestAccTestsResource_skips(t *testing.T) {
	repo := testRegistry(t, context.Background())
	t.Logf("serving ephemeral test registry at %s", repo)

	tpl := `
provider "imagetest" {
  test_execution = {
    include_by_label = %[3]s
    exclude_by_label = %[4]s
  }
}

resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "docker_in_docker"

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:4559395ca443fc5d7be4dc813370ef3de4dda8561d1b9a8a24dc578027339791"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "./%[1]s"
    }
  ]

  labels = %[2]s

  // Something before GHA timeouts
  timeout = "5m"
}
  `

	testCases := map[string][]resource.TestStep{
		"skipped-via-include": {
			{
				Config: fmt.Sprintf(tpl, "docker-in-docker-fails.sh", `{"foo":"bar"}`, `{"foo":"baz"}`, `{}`),
				Check:  resource.TestCheckResourceAttr("imagetest_tests.foo", "skipped", "true"),
			},
		},
		"skipped-via-exclude": {
			{
				Config: fmt.Sprintf(tpl, "docker-in-docker-fails.sh", `{"foo":"bar"}`, `{}`, `{"foo":"bar"}`),
				Check:  resource.TestCheckResourceAttr("imagetest_tests.foo", "skipped", "true"),
			},
		},
		"included-via-label": {
			{
				Config: fmt.Sprintf(tpl, "docker-in-docker-basic.sh", `{"foo":"bar"}`, `{"foo":"bar"}`, `{}`),
				Check:  resource.TestCheckResourceAttr("imagetest_tests.foo", "skipped", "false"),
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.Test(t, resource.TestCase{
				PreCheck: func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
					"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
						repo: repo,
					}),
				},
				Steps: tc,
			})
		})
	}
}

func TestIsLocalRegistry(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		want    bool
		wantErr bool
	}{
		{
			name:    "localhost with port",
			ref:     "localhost:5000/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "localhost without port",
			ref:     "localhost/myimage",
			want:    false, // No prefix match without ":"
			wantErr: false,
		},
		{
			name:    "domain.localhost with port",
			ref:     "myregistry.localhost:5000/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "domain.local with port",
			ref:     "myregistry.local:5000/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "domain.local without port",
			ref:     "myregistry.local/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "IPv4 loopback",
			ref:     "127.0.0.1/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "IPv4 loopback with port",
			ref:     "127.0.0.1:5000/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "IPv6 loopback",
			ref:     "::1/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "IPv6 loopback with port",
			ref:     "::1:5000/myimage",
			want:    true,
			wantErr: false,
		},
		{
			name:    "remote registry",
			ref:     "docker.io/library/ubuntu",
			want:    false,
			wantErr: false,
		},
		{
			name:    "remote registry with port",
			ref:     "gcr.io:443/google-containers/nginx",
			want:    false,
			wantErr: false,
		},
		{
			name:    "similar to local but not matching",
			ref:     "mylocalhost/image",
			want:    false,
			wantErr: false,
		},
		{
			name:    "IP similar to loopback but not matching",
			ref:     "127.0.0.2/image",
			want:    false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := name.ParseReference(tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Get the registry component from the parsed reference
			registry := ref.Context().Registry

			if got := isLocalRegistry(registry); got != tt.want {
				t.Errorf("isLocalRegistry() = %v, want %v for registry %s",
					got, tt.want, registry.Name())
			}
		})
	}
}
