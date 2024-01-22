terraform {
  required_providers {
    imagetest = { source = "registry.terraform.io/chainguard-dev/imagetest" }
  }
  backend "inmem" {}
}

provider "imagetest" {
  harnesses = {
    container = {
      envs = {
        scope = "global"
      }
    }
  }
}

data "imagetest_inventory" "this" {}

resource "imagetest_harness_container" "foo" {
  name      = "foo"
  inventory = data.imagetest_inventory.this

  envs = {
    name = "foo"
  }
}

resource "imagetest_feature" "foo" {
  name    = "Footure"
  harness = imagetest_harness_container.foo
  steps = [
    {
      name = "Sample"
      cmd  = "echo hello $name from $scope"
    },
  ]
}
