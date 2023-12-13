terraform {
  required_providers {
    imagetest = {
      source = "registry.terraform.io/joshrwolf/imagetest"
    }
  }
  backend "inmem" {}
}

provider "imagetest" {}

resource "imagetest_feature" "footure" {
  name        = "footure"
  description = "My great footure"

  assert {
    cmd = "echo 'first assertion'"
  }

  assert {
    cmd = "echo 'second assertion'"
  }
}

resource "imagetest_env" "foo" {
  test {
    features = [imagetest_feature.footure.id]
  }

  // Labels allow for identifying tests at runtime. Environments will only be
  // run if the runtime labels match the labels defined here.
  labels = {
    // Match this test with:
    // imagetest_LABELS="foo=bar" terraform apply
    // or
    // imagetest_LABELS="bar=baz" terraform apply
    // or
    // imagetest_LABELS="foo=bar,bar=baz" terraform apply
    "foo" = "bar"
    "bar" = "baz"
  }
}
