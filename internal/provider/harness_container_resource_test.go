package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHarnessContainerResource(t *testing.T) {
	testCases := map[string][]resource.TestStep{
		"basic container harness": {
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_container" "test" {
  name      = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name        = "Simple container based test"
  description = "Test that we can spin up a container and run some steps"
  harness     = imagetest_harness_container.test
  steps = [
    {
      name = "wolfi"
      cmd  = "cat /etc/os-release | grep -q 'wolfi'"
    },
  ]
}
        `,
			},
		},
		"with resource provider": {
			{
				ExpectNonEmptyPlan: true,
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

data "imagetest_inventory" "this" {}

resource "imagetest_harness_container" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  envs = {
    bar = "bar"
    baz = "baz"
  }
}

resource "imagetest_feature" "test" {
  name = "Simple container based test"
  description = "Test that we can spin up a container and run some steps"
  harness = imagetest_harness_container.test
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
		"with working directory": {
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_container" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "Simple container based test"
  description = "Test that we can spin up a container and run some steps with a working directory"
  harness = imagetest_harness_container.test
  steps = [
    {
      workdir = "/tmp"
      name = "Echo"
      cmd = "echo test >> .testfile"
    },
    {
      workdir = "/tmp"
      name = "Cat"
      cmd = "cat .testfile"
    }
  ]
}
        `,
				Check: resource.ComposeAggregateTestCheckFunc(),
			},
		},
		"with volumes configuration": {
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_container_volume" "volume" {
  name = "volume-test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_harness_container" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  volumes = [
    {
      source = imagetest_container_volume.volume
      destination = "/volume"
    }
  ]
}

resource "imagetest_feature" "test" {
  name = "Simple container based test"
  description = "Test that we can spin up a container and run some steps with a working directory"
  harness = imagetest_harness_container.test
  steps = [
    {
      workdir = "/tmp"
      name = "Echo"
      cmd = "echo test >> .testfile"
    },
    {
      workdir = "/tmp"
      name = "Cat"
      cmd = "cat .testfile"
    }
  ]
}
        `,
				Check: resource.ComposeAggregateTestCheckFunc(),
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps:                    tc,
			})
		})
	}
}
