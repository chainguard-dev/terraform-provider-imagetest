terraform {
  required_providers {
    imagetest = { source = "chainguard-dev/imagetest" }
  }
  backend "inmem" {}
}

# Expose variables to control test skipping during `terraform apply`
variable "skip_all_tests" {
  type    = bool
  default = false
}

variable "include_tests_by_label" {
  type    = map(string)
  default = {}
}

variable "skip_tests_by_label" {
  type    = map(string)
  default = {}
}

# Wire variables into imagetest provider configuration.
provider "imagetest" {
  test_execution = {
    skip_all_tests   = var.skip_all_tests
    include_by_label = var.include_tests_by_label
    exclude_by_label = var.skip_tests_by_label
  }
}

data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "k3s" {
  name      = "k3s-hello-world"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "k3s-hello-world" {
  name    = "k3s-hello-world"
  harness = imagetest_harness_k3s.k3s

  steps = [
    {
      name = "Hello (testing) world"
      cmd  = "echo 'This is a test step being ran'"
    }
  ]

  labels = {
    type  = "k8s"
    cloud = "any"
    size  = "small"
  }
}

resource "imagetest_feature" "k3s-hello-eks-world" {
  name    = "k3s-hello-world-eks"
  harness = imagetest_harness_k3s.k3s

  steps = [
    {
      name = "Hello ([EKS] testing) world"
      cmd  = "echo 'This is a test step being ran that can only be ran on EKS because I it is extreeeemely special'"
    }
  ]

  labels = {
    type  = "k8s"
    cloud = "aws"
    size  = "small"
    flaky = "true"
  }
}

resource "imagetest_harness_docker" "docker" {
  name      = "docker-hello-world"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "docker-hello-world" {
  name    = "docker-hello-world"
  harness = imagetest_harness_docker.docker

  steps = [
    {
      name = "Hello (testing) world"
      cmd  = "echo 'This is a test step being ran'"
    }
  ]

  labels = {
    type  = "docker"
    cloud = "any"
    size  = "small"
  }
}

resource "imagetest_feature" "docker-hello-eks-world" {
  name    = "docker-aws-hello-world"
  harness = imagetest_harness_docker.docker

  steps = [
    {
      name = "Hello ([AWS] testing) world"
      cmd  = "echo 'This is a test step being ran that can only be ran on AWS because I it is extreeeemely special'"
    }
  ]

  labels = {
    type  = "docker"
    cloud = "aws"
    size  = "small"
    flaky = "true"
  }
}