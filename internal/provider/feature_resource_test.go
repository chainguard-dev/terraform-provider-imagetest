package provider

import (
	"fmt"
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
				Config: testAccFeatureResourceConfig(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("imagetest_feature.test", "name", "DoATest"),
				),
			},
		},
	})
}

func testAccFeatureResourceConfig() string {
	return fmt.Sprintf(`
resource "imagetest_feature" "test" {
  name = "DoATest"
}
`)
}
