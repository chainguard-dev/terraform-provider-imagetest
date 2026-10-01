terraform {
  required_providers {
    imagetest = {
      source = "chainguard-dev/imagetest"
    }
  }
}

variable "vpc_id" {
  description = "A VPC with an internet gateway and public routing."
  type        = string
}

variable "repo" {
  description = "Repository to push the test sandbox image to."
  type        = string
  default     = "ttl.sh/imagetest-diagnostics"
}

variable "fail_in" {
  description = "Where to fail on purpose: \"setup\" or \"test\"."
  type        = string
  default     = "test"
  validation {
    condition     = contains(["setup", "test"], var.fail_in)
    error_message = "fail_in must be \"setup\" or \"test\"."
  }
}

provider "imagetest" {}

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

resource "imagetest_tests" "hello" {
  name   = "ec2-diagnostics"
  driver = "ec2"
  repo   = var.repo

  drivers = {
    ec2 = {
      vpc_id        = var.vpc_id
      ami           = "ami-01b52ecd9c0144a93" # Ubuntu 24.04 amd64
      instance_type = "t3.medium"
      user_data     = local.docker_cloud_init
      setup_commands = [
        # Leave behind a service log in a root-only directory and a script
        # transcript, like a real deploy would.
        "sudo mkdir -m 700 -p /var/log/hello && echo 'hello from a root-only log' | sudo tee /var/log/hello/hello.log >/dev/null",
        "echo 'setup transcript' > /tmp/setup.out",
        var.fail_in == "setup" ? "echo 'setup failed on purpose' >&2 && exit 1" : "true",
      ]

      # Run on the instance on failure, before it is torn down. The output is
      # reported as a warning.
      on_failure = [
        "sudo ls -la /var/log/hello",
        # Let a root shell expand the glob: the directory is root-only.
        "sudo sh -c 'cat /var/log/hello/*.log'",
        "cat /tmp/setup.out",
        # A failing command is reported and doesn't stop the others.
        "cat /does/not/exist",
      ]
    }
  }

  images = {
    test = local.busybox
  }

  tests = [{
    name  = "hello-world"
    image = local.busybox
    cmd   = var.fail_in == "test" ? "echo 'hello world' && exit 1" : "echo 'hello world'"
  }]

  timeout = "15m"
}
