terraform {
  required_providers {
    imagetest = {
      source = "registry.terraform.io/chainguard-dev/imagetest"
    }
    apko = { source = "chainguard-dev/apko" }
  }
  backend "inmem" {}
}

locals { repo = "gcr.io/wolf-chainguard/images" }

provider "imagetest" {
  repo = local.repo
}

provider "apko" {
  extra_repositories = concat([
    "https://packages.wolfi.dev/os",
    "https://packages.cgr.dev/extras",
  ])
  build_repositories = ["https://apk.cgr.dev/chainguard-private"]
  extra_keyring = concat([
    "https://packages.wolfi.dev/os/wolfi-signing.rsa.pub",
    "https://packages.cgr.dev/extras/chainguard-extras.rsa.pub",
  ])

  extra_packages = ["chainguard-baselayout"]

  default_archs = ["aarch64"]
}

module "helm_test" {
  source            = "./helm"
  target_repository = local.repo
  content           = "${path.module}/tests"
}

resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "k3s_in_docker"

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:b7fc3eef4303188eb295aaf8e02d888ced307d2a45090d6f673b95a41bfc033d"
  }

  tests = concat([], module.helm_test.test)
}

output "tests" {
  value = imagetest_tests.foo
}
