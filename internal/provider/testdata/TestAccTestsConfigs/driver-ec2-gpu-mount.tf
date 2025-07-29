locals {
  layers = {
    base = {
      image  = "cgr.dev/chainguard-private/pytorch"
      tag    = "latest-dev"
      digest = "sha256:959ea3da190962f31cb3fee4beb652e4bcebfccb17b4e8bd4e995442f1937824"
    }
    test = {
      image  = "cgr.dev/chainguard/busybox"
      tag    = "latest"
      digest = "sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
    }
  }
  nv_toolkit_version = "1.17.8-1"
}

resource "imagetest_tests" "foo" {
  name   = "driver-ec2-volume-mount"
  driver = "ec2"

  drivers = {
    ec2 = {
      # Ubuntu 24.04 (arm64)
      ami = "ami-0836fd4a4a0b4f6ec"
      # This _must_ be a 2xlarge.
      #
      # See bullet (2) in section 'Other Linux distributions' here:
      # https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/supported-platforms.html#other-linux-distributions
      instance_type  = "g5g.2xlarge"
      mount_all_gpus = true

      exec = {
        user  = "ubuntu"
        shell = "bash"
      }
    }
  }

  images = {
    foo = "${local.layers.base.image}:${local.layers.base.tag}@${local.layers.base.digest}"
  }

  tests = [{
    name  = "driver-ec2-volume-mount"
    image = "${local.layers.test.image}:${local.layers.test.tag}@${local.layers.test.digest}"
    cmd   = "nvidia-smi"
  }]

  // Something before GHA timeouts
  timeout = "30m"
}
