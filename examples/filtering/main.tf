terraform {
  required_providers {
    imagetest = { source = "chainguard-dev/imagetest" }
  }
  backend "inmem" {}
}

variable "runtime_labels" {
  type    = map(string)
  default = {}
}

provider "imagetest" {
  labels = var.runtime_labels
}

data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "foo" {
  name      = "foo"
  inventory = data.imagetest_inventory.this
  image     = "gcr.io/wolf-chainguard/images/k3s-test"
}

resource "imagetest_feature" "foo" {
  name    = "foo"
  harness = imagetest_harness_k3s.foo

  steps = [
    {
      name = "Sample"
      cmd  = "sleep inf"
    }
  ]

  labels = {
    type  = "k8s"
    cloud = "any"
  }
}
