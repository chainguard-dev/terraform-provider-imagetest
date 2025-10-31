# driver-ec2-volume-mount.tf implements an acceptance test of the 'ec2' driver.
#
# Test workflow:
#
# 1. During the in-driver commands, create directory '/data' in the host, and
#    echo 'Hello, world!' to to '/data/test'.
#    a. The driver config also specifies a Docker bind mount '/data:/data'.
# 2. During the test, we 'cat' '/data/test' and confirm the value we receive
#    back is 'Hello, world!'.
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
  name   = "driver-ec2-volume-mount"
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
          # Place a file on the host and bind mount it to the container.
          "sudo mkdir -m 777 -p /data",
          "echo -n 'Hello, world!' | sudo tee /data/test",
          "sudo chmod 666 /data/test"
        ]
      }

      volume_mounts = [
        "/data:/data"
      ]
    }
  }

  images = {
    foo = "${local.layers.base.image}:${local.layers.base.tag}@${local.layers.base.digest}"
  }

  tests = [{
    name  = "driver-ec2-volume-mount"
    image = "${local.layers.test.image}:${local.layers.test.tag}@${local.layers.test.digest}"
    cmd   = "[ -f /data/test ]"
  }]

  timeout = "10m"
}
