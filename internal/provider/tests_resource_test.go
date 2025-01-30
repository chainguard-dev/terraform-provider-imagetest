package provider

import (
	"context"
	"fmt"
	"os"
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

	testCases := map[string][]resource.TestStep{
		"basic":          {{Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-basic.sh")}},
		"non-executable": {{Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-non-executable.sh")}},
		// ensure command's exit code surfaces in tf error
		"fails-with-proper-exit-code": {
			{
				Config:      fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-fails-with-proper-exit-code.sh"),
				ExpectError: regexp.MustCompile(`.*213.*`),
			},
		},
		// ensures set -eux is always plumbed through and command stderr is surfaced in tf error
		"fails-with-bad-command": {
			{
				Config:      fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-fails-with-bad-command.sh"),
				ExpectError: regexp.MustCompile(`.*No such file or directory.*`),
			},
		},
		// TODO: This test will leave the clusters dangling and needs to be
		// manually cleaned up.
		"enters-debug-mode-with-proper-exit-code-on-failure": {
			{
				Config:      fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-fails-with-proper-exit-code.sh"),
				ExpectError: regexp.MustCompile(`.*213.*`),
				PreConfig: func() {
					if err := os.Setenv("IMAGETEST_SKIP_TEARDOWN", "true"); err != nil {
						t.Fatal(err)
					}
				},
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
