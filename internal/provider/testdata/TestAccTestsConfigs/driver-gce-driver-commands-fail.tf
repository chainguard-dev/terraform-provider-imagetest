# driver-gce-driver-commands-fail.tf tests that setup command failures are surfaced.

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

resource "imagetest_tests" "driver_commands_fail" {
  name   = "driver-gce-driver-commands-fail"
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
        "exit 1",
      ]
    }
  }

  images = {
    test = local.busybox
  }

  tests = [{
    name  = "test"
    image = local.busybox
    cmd   = "echo should-not-reach"
  }]

  timeout = "10m"
}
