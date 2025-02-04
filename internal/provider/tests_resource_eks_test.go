//go:build eks
// +build eks

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource_EKS(t *testing.T) {
	repo := "ttl.sh/imagetest" // TODO: Don't push to ttl.sh

	k3sindockerTpl := `
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "eks_with_eksctl"

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

  // Creating the cluster takes ~15m... 🐌
  timeout = "20m"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
				repo: repo,
			}),
		},
		Steps: []resource.TestStep{
			{Config: fmt.Sprintf(k3sindockerTpl, "k3s-in-docker-basic.sh")},
		},
	})
}
