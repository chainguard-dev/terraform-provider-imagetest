terraform {
  required_providers {
    imagetest = {
      source = "registry.terraform.io/chainguard-dev/imagetest"
    }
    apko = { source = "chainguard-dev/apko" }
  }
  backend "inmem" {}
}

# Create an testing inventory. This is used under the hood to keep track of the
# harnesses and features. At least one inventory is required and is plumbed
# through to the dependent harnesses.
data "imagetest_inventory" "this" {}

# Define a testing harness and register it with the inventory.
resource "imagetest_harness_container" "foo" {
  name      = "foo"
  inventory = data.imagetest_inventory.this
}

# Define a feature that operates on the given harness. Depending on the
# harness, features will execute their steps in different environments. In this
# example, the features are executed in a sandbox container configured in the
# `harness_container` resource.
resource "imagetest_feature" "foo" {
  name    = "Footure"
  harness = imagetest_harness_container.foo
  steps = [
    {
      name = "Sample"
      cmd  = "apk add curl"
    },
  ]
}
