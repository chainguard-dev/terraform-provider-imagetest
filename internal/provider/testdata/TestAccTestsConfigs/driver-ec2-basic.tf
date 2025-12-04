# driver-ec2-basic.tf tests basic EC2 driver functionality.
#
# Verifies:
# - Instance provisioning with cloud-init
# - setup_commands execute in persistent shell session
# - Container runs and exits successfully

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

resource "imagetest_tests" "basic" {
  name   = "driver-ec2-basic"
  driver = "ec2"

  drivers = {
    ec2 = {
      vpc_id        = var.vpc_id
      ami           = "ami-01b52ecd9c0144a93" # Ubuntu 24.04 amd64
      instance_type = "t3.medium"
      user_data     = local.docker_cloud_init
      setup_commands = [
        "VAR=hello",
        "[ \"$VAR\" = hello ]",
      ]
    }
  }

  images = {
    test = local.busybox
  }

  tests = [{
    name  = "basic"
    image = local.busybox
    cmd   = "echo success"
  }]

  timeout = "10m"
}
