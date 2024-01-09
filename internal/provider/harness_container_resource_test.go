package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHarnessContainerResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create testing
			{
				Config: `
			resource "imagetest_harness_container" "test" {}
			resource "imagetest_harness_teardown" "test" { harness = imagetest_harness_container.test.id }
			resource "imagetest_feature" "test" {
			  name = "Simple container based test"
			  description = "Test that we can spin up a container and run some steps"
			  harness = imagetest_harness_container.test.id
			  steps = [
			    {
			      name = "Echo"
			      cmd = "echo hello world"
			    },
			  ]
			}
			          `,
				Check: resource.ComposeAggregateTestCheckFunc(),
			},
		},
	})
}

func TestAccHarnessContainerResourceProvider(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "imagetest" {
  harnesses = {
    container = {
      envs = {
        foo = "foo"
        baz = "override"
      }
    }
  }
}
resource "imagetest_harness_container" "test" { envs = { "bar" = "bar", "baz" = "baz" }}
resource "imagetest_harness_teardown" "test" { harness = imagetest_harness_container.test.id }
resource "imagetest_feature" "test" {
  name = "Simple container based test"
  description = "Test that we can spin up a container and run some steps"
  harness = imagetest_harness_container.test.id
  steps = [
    {
      name = "Echo"
      cmd = "echo $foo $bar $baz | diff - <(echo foo bar baz) > /dev/null"
    },
  ]
}
        `,
				Check: resource.ComposeAggregateTestCheckFunc(),
			},
		},
	})
}
