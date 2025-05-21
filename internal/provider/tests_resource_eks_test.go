//go:build eks
// +build eks

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource_EKS(t *testing.T) {
	nodeAMI := os.Getenv("IMAGETEST_EKS_NODE_AMI")

	repo := "ttl.sh/imagetest" // TODO: Don't push to ttl.sh

	tf := fmt.Sprintf(`
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "eks_with_eksctl"

  drivers = {
    eks_with_eksctl = {
      node_ami = %q
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:07d60d734cbfb135653ba8a0823b2d5b6b2b68b248912ba624470de9926294bf"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev@sha256:1d8c1f0c437628aafa1bca52c41ff310aea449423cce9b2feae2767ac53c336f"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "/imagetest/k3s-in-docker-basic.sh"
    }
  ]

  // Creating the cluster takes ~15m... 🐌
  timeout = "30m"
}
`, nodeAMI)

	// Test with storage configuration
	tfWithStorage := fmt.Sprintf(`
resource "imagetest_tests" "foo_with_storage" {
  name   = "foo-with-storage"
  driver = "eks_with_eksctl"

  drivers = {
    eks_with_eksctl = {
      node_ami = %q
      node_type = "m5.4xlarge"
      node_count = 2
      storage = {
        size = "20GB"
        type = "gp3"
      }
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:07d60d734cbfb135653ba8a0823b2d5b6b2b68b248912ba624470de9926294bf"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev@sha256:1d8c1f0c437628aafa1bca52c41ff310aea449423cce9b2feae2767ac53c336f"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "/imagetest/k3s-in-docker-basic.sh"
    }
  ]

  // Creating the cluster takes ~15m... 🐌
  timeout = "30m"
}
`, nodeAMI)

	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
				repo: repo,
			}),
		},
		Steps: []resource.TestStep{
			{Config: tf},
			{Config: tfWithStorage},
		},
	})
}
