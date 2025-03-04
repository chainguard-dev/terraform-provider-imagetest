package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

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
    foo = "cgr.dev/chainguard/busybox:latest@sha256:98fa8044785ff59248ec9e5747bff259c6fe4b526ebb77d95d8a98ad958847dd"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev@sha256:1d8c1f0c437628aafa1bca52c41ff310aea449423cce9b2feae2767ac53c336f"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "/imagetest/%s"
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
    foo = "cgr.dev/chainguard/busybox:latest@sha256:98fa8044785ff59248ec9e5747bff259c6fe4b526ebb77d95d8a98ad958847dd"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/busybox:latest"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "/imagetest/%s"
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
    foo = "cgr.dev/chainguard/busybox:latest@sha256:98fa8044785ff59248ec9e5747bff259c6fe4b526ebb77d95d8a98ad958847dd"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev@sha256:1d8c1f0c437628aafa1bca52c41ff310aea449423cce9b2feae2767ac53c336f"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "/imagetest/%s"
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
    foo = "cgr.dev/chainguard/busybox:latest@sha256:98fa8044785ff59248ec9e5747bff259c6fe4b526ebb77d95d8a98ad958847dd"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev@sha256:1d8c1f0c437628aafa1bca52c41ff310aea449423cce9b2feae2767ac53c336f"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "/imagetest/%[1]s"
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
