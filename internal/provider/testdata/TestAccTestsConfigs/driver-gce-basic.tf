# driver-gce-basic.tf tests basic GCE driver functionality.
#
# Verifies:
# - Instance provisioning with startup-script
# - setup_commands execute in persistent shell session
# - Container runs and exits successfully

variable "project_id" {
  type = string
}

variable "zone" {
  type    = string
  default = "us-west1-b"
}

variable "network" {
  type    = string
  default = "default"
}

locals {
  busybox = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
}

resource "imagetest_tests" "basic" {
  name   = "driver-gce-basic"
  driver = "gce"

  drivers = {
    gce = {
      project_id   = var.project_id
      zone         = var.zone
      network      = var.network
      image        = "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts"
      machine_type = "n1-standard-2"
      startup_script = <<-EOF
        #!/bin/bash
        apt-get update && apt-get install -y docker.io
        systemctl enable docker && systemctl start docker
        usermod -aG docker ubuntu
      EOF
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
