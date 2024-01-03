terraform {
  required_providers {
    imagetest = {
      source = "registry.terraform.io/chainguard-dev/imagetest"
    }
  }
  backend "inmem" {}
}

provider "imagetest" {}

# Create a harness that runs features in a container.
resource "imagetest_harness_container" "this" {
  image = "cgr.dev/chainguard/wolfi-base:latest"
  mounts = [
    {
      source      = path.module
      destination = "/src"
    }
  ]
}
resource "imagetest_harness_teardown" "container" { harness = imagetest_harness_container.this.id }

resource "imagetest_feature" "container" {
  name        = "Simple container based test"
  description = "A simple test that uses a container harness to create a container and run a set of tests."

  harness = imagetest_harness_container.this.id

  steps = [
    {
      name = "Install something"
      cmd  = <<EOF
        apk add curl
      EOF
    },
    {
      name = "Access files we mounted from the host"
      cmd  = <<EOF
        ls -lah /src
      EOF
    },
  ]

  labels = {
    size = "small"
    type = "container"
  }
}

# Create a harness that runs k3s in in a service container, and runs features
# in a network attached container with pre-installed k8s related packages (like
# kubectl).
resource "imagetest_harness_k3s" "this" {}
resource "imagetest_harness_teardown" "k3s" { harness = imagetest_harness_k3s.this.id }

resource "imagetest_feature" "k3s" {
  name        = "Simple k3s based test"
  description = "A simple test that uses a k3s harness to run a k8s based test."

  harness = imagetest_harness_k3s.this.id

  steps = [
    {
      name = "Interact with the service cluster"
      cmd  = <<EOF
        kubectl get no
        kubectl get po -A
      EOF
    },
  ]

  labels = {
    cloud = "any"
    size  = "medium"
    type  = "k8s"
  }
}
