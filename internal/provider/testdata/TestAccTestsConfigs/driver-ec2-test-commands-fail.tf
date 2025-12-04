# driver-ec2-test-commands-fail.tf tests that container failures are caught.
#
# Verifies:
# - Container exit code is checked
# - Non-zero exit fails the test

variable "vpc_id" {
  type = string
}

locals {
  docker_cloud_init = <<-EOF
    #cloud-config
    packages:
      - docker.io
    runcmd:
      - systemctl enable docker
      - systemctl start docker
      - usermod -aG docker ubuntu
  EOF

  busybox = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
}

resource "imagetest_tests" "test_fail" {
  name   = "driver-ec2-test-commands-fail"
  driver = "ec2"

  drivers = {
    ec2 = {
      vpc_id        = var.vpc_id
      ami           = "ami-01b52ecd9c0144a93" # Ubuntu 24.04 amd64
      instance_type = "t3.medium"
      user_data     = local.docker_cloud_init
    }
  }

  images = {
    test = local.busybox
  }

  tests = [{
    name  = "fail"
    image = local.busybox
    cmd   = "exit 1"
  }]

  timeout = "10m"
}
