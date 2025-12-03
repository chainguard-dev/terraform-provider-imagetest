# driver-ec2-volume-mount.tf tests Docker volume mounts.
#
# Verifies:
# - volume_mounts config is passed to Docker
# - Host files are accessible in container

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

resource "imagetest_tests" "volume_mount" {
  name   = "driver-ec2-volume-mount"
  driver = "ec2"

  drivers = {
    ec2 = {
      vpc_id        = var.vpc_id
      ami           = "ami-01b52ecd9c0144a93" # Ubuntu 24.04 amd64
      instance_type = "t3.medium"
      user_data     = local.docker_cloud_init
      setup_commands = [
        "sudo mkdir -p /data",
        "echo hello | sudo tee /data/test",
      ]
      volume_mounts = ["/data:/data"]
    }
  }

  images = {
    test = local.busybox
  }

  tests = [{
    name  = "volume-mount"
    image = local.busybox
    cmd   = "cat /data/test | grep hello"
  }]

  timeout = "10m"
}
