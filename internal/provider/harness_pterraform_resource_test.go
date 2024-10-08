package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHarnessPterraformResource(t *testing.T) {
	t.Parallel()

	testCases := map[string][]resource.TestStep{
		"local docker connector": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_pterraform" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  path = "./testdata/pterraform/docker"
  vars = jsonencode({ foo = "notbar" })
}

resource "imagetest_feature" "test" {
  name = "Simple pterraform based test with local docker connector"
  description = "Test that we can spin up a pterraform resource and run some steps"
  harness = imagetest_harness_pterraform.test
  steps = [
    {
      name = "Make sure we can hit the cluster we're on"
      cmd = "grep -q 'wolfi' /etc/os-release"
    },
    {
      name = "Make sure variables are passed through"
      cmd = "echo $FOO | grep -q 'notbar'"
    },
  ]
}
          `,
			},
		},
		"kubernetes connector via k3s in docker": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_pterraform" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  path = "./testdata/pterraform/local-k8s"
}

resource "imagetest_feature" "test" {
  name = "Simple pterraform based test"
  description = "Test that we can spin up a pterraform resource and run some steps"
  harness = imagetest_harness_pterraform.test
  steps = [
    {
      name = "Install some stuff"
      cmd = "apk add kubectl"
    },
    {
      name = "Access and create some stuff"
      cmd = "kubectl get po -A && kubectl create ns test"
    },
  ]
}
          `,
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps:                    tc,
			})
		})
	}
}
