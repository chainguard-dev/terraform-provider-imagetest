# driver-ec2-volume-mount.tf implements an acceptance test of the 'ec2' driver.
#
# Test workflow:
#
# 1. Provision a GPU-enabled ARM64 instance type ('g5g.2xlarge').
# 2. Install the NVIDIA 575 GPU driver and related libraries via 'cloud-init'
#    userdata.
# 3. Specify the 'mount_all_gpus' driver configuration setting to make the host
#    GPU visible to the container.
# 4. Run a GPU-enabled image (Pytorch) and confirm 'nvidia-smi' exits cleanly.
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
}

resource "imagetest_tests" "foo" {
  name   = "driver-ec2-volume-mount"
  driver = "ec2"

  drivers = {
    ec2 = {
      # Canonical's Ubuntu 24.04, arm64
      ami = "ami-0a19bfd91e88a454b"
      # This _must_ be a 2xlarge.
      #
      # See bullet (2) in section 'Other Linux distributions' here:
      # https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/supported-platforms.html#other-linux-distributions
      instance_type  = "g5g.2xlarge"
      mount_all_gpus = true

      exec = {
        user      = "ubuntu"
        shell     = "bash"
        user_data = <<EOF
#cloud-config

package_update: true
package_upgrade: true
package_reboot_if_required: true

runcmd:
- [ sh, -c, "apt install -y -qqq linux-modules-nvidia-575-server-open-$(uname -r) nvidia-driver-575-server-open" ]
- [ modprobe, nvidia ]
EOF
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

  timeout = "20m"
}
