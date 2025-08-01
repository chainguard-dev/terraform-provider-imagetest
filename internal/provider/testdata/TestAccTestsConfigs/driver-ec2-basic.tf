# driver-ec2-volume-mount.tf implements an acceptance test of the 'ec2' driver.
#
# Test workflow:
#
# 1. Check arbitrary variable equality across two entries in the driver
#    configuration's 'exec.commands' list (these commands are executed within
#    a persistent shell session so variables defined in one step should persist
#    in the next).
# 2. Simply echo 'Hello, World!' for the test.
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
  name   = "driver-ec2-basic"
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
          # These two commands are just a silly little example to demonstrate
          # the persistence of state across commands since everything in
          # 'commands' is executed within the scope of a single SSH session.
          "some=1337",
          "[ $some -eq 1337 ] && exit 0 || exit 1",
        ]
      }
    }
  }

  images = {
    foo = "${local.layers.base.image}:${local.layers.base.tag}@${local.layers.base.digest}"
  }

  tests = [{
    name  = "driver-ec2-basic"
    image = "${local.layers.test.image}:${local.layers.test.tag}@${local.layers.test.digest}"
    cmd   = "echo 'Hello, world!'; exit 0"
  }]

  timeout = "10m"
}
