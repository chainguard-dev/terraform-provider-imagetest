# driver-ec2-availability-zone.tf tests explicit availability zone configuration
#
# Test workflow:
#
# 1. Launch EC2 instance with explicit availability_zone set to us-west-2a
# 2. Verify that the subnet is created in the specified availability zone
# 3. Run a simple test to verify the infrastructure works correctly

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

resource "imagetest_tests" "availability_zone" {
  name   = "driver-ec2-availability-zone"
  driver = "ec2"

  drivers = {
    ec2 = {
      # Canonical's Ubuntu 24.04, amd64
      ami               = "ami-01b52ecd9c0144a93"
      instance_type     = "t3.medium"
      availability_zone = "us-west-2a"

      exec = {
        user  = "ubuntu"
        shell = "bash"
        commands = [
          # Query the instance metadata to verify we're in the correct AZ
          "INSTANCE_AZ=$(curl -s http://169.254.169.254/latest/meta-data/placement/availability-zone)",
          "echo \"Instance is running in AZ: $INSTANCE_AZ\"",

          # Verify we're in us-west-2a (not us-west-2d or other zones)
          "if [ \"$INSTANCE_AZ\" = \"us-west-2a\" ]; then",
          "  echo \"SUCCESS: Instance is in the correct availability zone (us-west-2a)\"",
          "  exit 0",
          "else",
          "  echo \"ERROR: Instance is in wrong availability zone: $INSTANCE_AZ (expected us-west-2a)\"",
          "  exit 1",
          "fi",
        ]
      }
    }
  }

  images = {
    test_image = "${local.layers.base.image}:${local.layers.base.tag}@${local.layers.base.digest}"
  }

  tests = [{
    name  = "availability-zone-test"
    image = "${local.layers.test.image}:${local.layers.test.tag}@${local.layers.test.digest}"
    cmd   = "echo 'Availability zone test passed!'; exit 0"
  }]

  timeout = "15m"
}
