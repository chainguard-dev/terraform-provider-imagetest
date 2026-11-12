variable "digest" {
  description = "The image digest to run tests over."
}

variable "target_repository" {}

variable "java-source-version" {
  type        = number
  description = "The Java source version to use for compilation in this test."
  default     = 8
}

variable "java-target-version" {
  type        = number
  description = "The Java target version to use for compilation in this test."
  default     = 8
}

variable "sdk-image" {
  description = "The SDK image to use for building the java application."
  default     = "cgr.dev/chainguard/jdk"
}

module "bash_sandbox" {
  source            = "../../../tflib/imagetest/sandboxes/bash"
  target_repository = var.target_repository
}

module "dind_test" {
  source = "../../../tflib/imagetest/tests/docker-in-docker"

  images = { jre = var.digest }

  tests = [
    {
      name    = "basic test"
      image   = module.bash_sandbox.image_ref
      content = [{ source = path.module }]
      cmd     = "./test.sh"
      envs = {
        SDK_IMAGE           = var.sdk-image
        JAVA_SOURCE_VERSION = var.java-source-version
        JAVA_TARGET_VERSION = var.java-target-version
      }
    }
  ]
}
