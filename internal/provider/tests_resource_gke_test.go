//go:build gke

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource_GKE(t *testing.T) {
	projectID := os.Getenv("IMAGETEST_GKE_PROJECT")
	region := os.Getenv("IMAGETEST_GKE_REGION")
	if region == "" {
		region = "us-central1"
	}

	repo := "ttl.sh/imagetest" // TODO: Don't push to ttl.sh

	// Test 1: Basic cluster creation
	tf := fmt.Sprintf(`
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "gke"

  drivers = {
    gke = {
      project    = %q
      region     = %q
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
      cmd     = "echo success"
    }
  ]

  timeout = "45m"
}
`, projectID, region)

	// Test 2: Custom node configuration
	tfWithCustomNodes := fmt.Sprintf(`
resource "imagetest_tests" "foo_custom" {
  name   = "foo-custom"
  driver = "gke"

  drivers = {
    gke = {
      project      = %q
      region       = %q
      node_count   = 2
      machine_type = "n1-standard-4"
      disk_size_gb = 150
      disk_type    = "pd-ssd"
      tags = {
        "team"        = "platform"
        "environment" = "test"
      }
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
      cmd     = "echo success"
    }
  ]

  timeout = "45m"
}
`, projectID, region)

	// Test 3: Zonal cluster
	zone := region + "-a"
	tfZonal := fmt.Sprintf(`
resource "imagetest_tests" "foo_zonal" {
  name   = "foo-zonal"
  driver = "gke"

  drivers = {
    gke = {
      project    = %q
      zone       = %q
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/kubectl:latest-dev@sha256:1d8c1f0c437628aafa1bca52c41ff310aea449423cce9b2feae2767ac53c336f"
      cmd     = "kubectl get nodes"
    }
  ]

  timeout = "45m"
}
`, projectID, zone)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			if projectID == "" {
				t.Fatal("IMAGETEST_GKE_PROJECT must be set for acceptance tests")
			}
		},
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
				repo: repo,
			}),
		},
		Steps: []resource.TestStep{
			{Config: tf},
			{Config: tfWithCustomNodes},
			{Config: tfZonal},
		},
	})
}
