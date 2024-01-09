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
				Config: `
resource "imagetest_harness_container" "test" {}
resource "imagetest_harness_teardown" "test" { harness = imagetest_harness_container.test.id }
resource "imagetest_feature" "test" {
  name = "Ordering"
  description = "Test the step ordering"
  harness = imagetest_harness_container.test.id
  before = [
    {
      name = "1"
      cmd = "echo 1 >> /tmp/feature_test"
    },
  ]
  after = [
    {
      name = "3"
      cmd = "echo 3 >> /tmp/feature_test"
    },
    {
      name = "assert"
      cmd = <<EOF
        cat /tmp/feature_test
        echo -e "1\n2\n3" | diff - /tmp/feature_test > /dev/null
      EOF
    },
  ]
  steps = [
    {
      name = "2"
      cmd = "echo 2 >> /tmp/feature_test"
    },
  ]
}
        `,
				Check: resource.ComposeAggregateTestCheckFunc(),
			},
		},
	})
}
