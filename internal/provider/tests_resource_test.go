package provider

import (
	"archive/tar"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

//go:embed testdata/TestAccTestsConfigs/k3s-in-docker-template.tf
var k3sindockerTpl string

//go:embed testdata/TestAccTestsConfigs/docker-in-docker-template.tf
var dockerindockerTpl string

//go:embed testdata/TestAccTestsConfigs/k3s-in-docker-hooks.tf
var k3sInDockerHooks string

//go:embed testdata/TestAccTestsConfigs/k3s-in-docker-artifacts.tf
var k3sInDockerArtifacts string

//go:embed testdata/TestAccTestsConfigs/k3s-in-docker-artifacts-on-failure.tf
var k3sInDockerArtifactsOnFailure string

//go:embed testdata/TestAccTestsConfigs/docker-in-docker-artifacts.tf
var dockerInDockerArtifacts string

func TestAccTestsResource(t *testing.T) {
	repo := testRegistry(t, context.Background()) //nolint: usetesting
	t.Logf("serving ephemeral test registry at %s", repo)

	testCases := map[string][]resource.TestStep{
		"k3sindocker-basic": {
			{
				Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-basic.sh"),
			},
		},
		"k3sindocker-non-executable": {
			{
				Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-non-executable.sh"),
			},
		},
		"k3sindocker-default-namespace": {
			{
				Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-default-namespace.sh"),
			},
		},
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
		"k3sindocker-hooks": {
			{
				Config: fmt.Sprintf(k3sInDockerHooks, "k3s-in-docker-hooks.sh"),
			},
		},
		"dockerindocker-basic": {
			{
				Config: fmt.Sprintf(dockerindockerTpl, "docker-in-docker-basic.sh"),
			},
		},
		"dockerindocker-fails-message": {
			{
				Config:      fmt.Sprintf(dockerindockerTpl, "docker-in-docker-fails.sh"),
				ExpectError: regexp.MustCompile(`.*can't open 'imalittleteapot'.*`),
			},
		},
		"k3sindocker-artifacts": {
			{
				Config: fmt.Sprintf(k3sInDockerArtifacts, "artifact.sh"),
				Check:  checkArtifact(t),
			},
		},
		"k3sindocker-artifacts-on-failure": {
			{
				ExpectError: regexp.MustCompile(`.*can't open 'imalittleteapot'.*`),
				Config:      fmt.Sprintf(k3sInDockerArtifactsOnFailure, "artifact-with-failure.sh"),
				Check:       checkArtifact(t),
			},
		},
		"dockerindocker-artifacts": {
			{
				Config: fmt.Sprintf(dockerInDockerArtifacts, "artifact.sh"),
				Check:  checkArtifact(t),
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

func checkArtifact(t *testing.T) func(s *terraform.State) error {
	return func(s *terraform.State) error {
		rname := "imagetest_tests.foo"
		rs, ok := s.RootModule().Resources[rname]
		if !ok {
			return fmt.Errorf("resource not found: %s", rname)
		}

		t.Logf("rs: %#v", rs.Primary.Attributes)

		auri, ok := rs.Primary.Attributes["tests.0.artifact.uri"]
		if !ok {
			return fmt.Errorf("attribute not found: %s", "tests.0.artifact.uri")
		} else if auri == "" {
			return fmt.Errorf("attribute value is empty: %s", "tests.0.artifact.uri")
		}

		achecksum, ok := rs.Primary.Attributes["tests.0.artifact.checksum"]
		if !ok {
			return fmt.Errorf("attribute not found: %s", "tests.0.artifact.checksum")
		} else if achecksum == "" {
			return fmt.Errorf("attribute value is empty: %s", "tests.0.artifact.checksum")
		}

		aurl, err := url.Parse(auri)
		if err != nil {
			return fmt.Errorf("failed to parse artifact URI: %w", err)
		}

		if aurl.Scheme != "file" {
			return fmt.Errorf("expected artifact scheme to be file, got %s", aurl.Scheme)
		}

		t.Logf("exfiltrated artifact uri: %s", auri)

		// load the tgz and verify its contents match what we expect
		af, err := os.Open(aurl.EscapedPath())
		if err != nil {
			return fmt.Errorf("failed to open artifact file: %w", err)
		}
		defer af.Close()

		gz, err := gzip.NewReader(af)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gz.Close()

		tr := tar.NewReader(gz)

		match := false
		expectedContent := "hello artifact content 123\n"
		var content []byte

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}

			if err != nil {
				return fmt.Errorf("failed to read tar header: %w", err)
			}

			if hdr.Typeflag != tar.TypeReg {
				continue
			}

			if hdr.Name != "results/output.txt" {
				continue
			}

			match = true

			content, err = io.ReadAll(tr)
			if err != nil {
				return fmt.Errorf("failed to read file content: %w", err)
			}

			break
		}

		if !match {
			return fmt.Errorf("expected artifact to contain results/output.txt")
		}

		if diff := cmp.Diff(expectedContent, string(content)); diff != "" {
			return fmt.Errorf("unexpected output.txt content (-want +got):\n%s", diff)
		}

		return nil
	}
}

func TestAccTestsResource_skips(t *testing.T) {
	repo := testRegistry(t, context.Background()) //nolint: usetesting
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
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
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
