# driver-ec2-volume-mount.tf implements an acceptance test of the 'ec2' driver.
#
# Test workflow:
#
# 1. Send an 'exit 1' in the driver configuration 'exec.commands' to ensure this
#    gets properly received as a failure.
locals {
  layers = {
    base = {
      image  = "cgr.dev/chainguard/busybox"
      tag    = "latest"
      digest = "sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
    }
    test = {
      image  = "cgr.dev/chainguard/busybox"
      tag    = "latest"
      digest = "sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
    }
  }
}

resource "imagetest_tests" "foo" {
  name   = "driver-ec2-driver-commands-fail"
  driver = "ec2"

  drivers = {
    ec2 = {
      # Canonical's Ubuntu 24.04, amd64
      ami           = "ami-01b52ecd9c0144a93"
      instance_type = "t3.xlarge"

      exec = {
        user  = "ubuntu"
        shell = "bash"
        commands = [
          "exit 1",
        ]
      }
    }
  }

  images = {
    foo = "${local.layers.base.image}:${local.layers.base.tag}@${local.layers.base.digest}"
  }

  tests = [{
    name  = "driver-ec2-driver-commands-fail"
    image = "${local.layers.test.image}:${local.layers.test.tag}@${local.layers.test.digest}"
    cmd   = "echo 'Hello, world!'; exit 0"
  }]

  timeout = "10m"
}
