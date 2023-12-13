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
}
