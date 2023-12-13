terraform {
  required_providers {
    imagetest = {
      source = "registry.terraform.io/joshrwolf/imagetest"
    }
  }
  backend "inmem" {}
}

provider "imagetest" {}

resource "imagetest_harness_k3s" "simple" {}

resource "imagetest_harness_teardown" "simple" {
  harness = imagetest_harness_k3s.simple.id
}

resource "imagetest_feature" "footure" {
  name        = "footure"
  description = "My great footure"

  setup {
    cmd = "echo 'setting up'"
  }

  teardown {
    cmd = "echo 'tearing down'"
  }

  assert {
    cmd = "echo 'assert 1'"
  }

  assert {
    cmd = "sleep 2"
  }

  assert {
    cmd = "kubectl get po -A"
  }
}

resource "imagetest_env" "foo" {
  harness = imagetest_harness_k3s.simple.id

  test {
    features = [imagetest_feature.footure.id]
  }
}
