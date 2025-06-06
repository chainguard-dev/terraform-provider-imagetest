package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccContainerVolumeResource(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProviderWithRegistry(t, context.Background()), //nolint: usetesting
		Steps: []resource.TestStep{
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_container_volume" "test" {
  name      = "test"
  inventory = data.imagetest_inventory.this
}
        `,
			},
		},
	})
}
