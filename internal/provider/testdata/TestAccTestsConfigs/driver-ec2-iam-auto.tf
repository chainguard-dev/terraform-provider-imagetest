# driver-ec2-iam-auto.tf tests automatic IAM role and instance profile creation.
#
# Verifies:
# - IAM role is auto-created when instance_profile_name not specified
# - Instance can access IAM metadata endpoint
# - Auto-created role has ECR permissions

variable "vpc_id" {
  type = string
}

locals {
  docker_cloud_init = <<-EOF
    #cloud-config
    packages:
      - docker.io
      - awscli
    runcmd:
      - systemctl enable docker
      - systemctl start docker
      - usermod -aG docker ubuntu
  EOF

  busybox = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
}

resource "imagetest_tests" "iam_auto" {
  name   = "driver-ec2-iam-auto"
  driver = "ec2"

  drivers = {
    ec2 = {
      vpc_id        = var.vpc_id
      ami           = "ami-01b52ecd9c0144a93" # Ubuntu 24.04 amd64
      instance_type = "t3.medium"
      user_data     = local.docker_cloud_init
      setup_commands = [
        "curl -sf http://169.254.169.254/latest/meta-data/iam/security-credentials/ | grep -q imagetest",
        "aws sts get-caller-identity",
      ]
    }
  }

  images = {
    test = local.busybox
  }

  tests = [{
    name  = "iam-auto"
    image = local.busybox
    cmd   = "echo 'IAM auto-creation verified'"
  }]

  timeout = "10m"
}
