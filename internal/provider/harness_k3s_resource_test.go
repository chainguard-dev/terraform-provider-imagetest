package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHarnessK3sResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create testing
			{
				Config: `
resource "imagetest_harness_k3s" "test" {}
resource "imagetest_harness_teardown" "test" { harness = imagetest_harness_k3s.test.id }
resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that we can spin up a k3s cluster and run some steps"
  harness = imagetest_harness_k3s.test.id
  steps = [
    {
      name = "Access cluster"
      cmd = "kubectl get po -A"
    },
  ]
}
          `,
				Check: resource.ComposeAggregateTestCheckFunc(),
			},
		},
	})
}
