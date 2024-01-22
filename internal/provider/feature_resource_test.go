package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccFeatureResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and read testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_container" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "Ordering"
  description = "Test the step ordering"
  harness = imagetest_harness_container.test
  before = [
    {
      name = "1"
      cmd = "echo first >> /tmp/feature_test"
    },
  ]
  after = [
    {
      name = "3"
      cmd = "echo third >> /tmp/feature_test"
    },
    {
      name = "assert"
      cmd = <<EOF
        cat /tmp/feature_test
        echo -e "first\nsecond\nthird" | diff - /tmp/feature_test > /dev/null
      EOF
    },
  ]
  steps = [
    {
      name = "2"
      cmd = "echo second >> /tmp/feature_test"
    },
  ]
}
        `,
			},
		},
	})
}
