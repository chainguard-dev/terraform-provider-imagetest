terraform {
  required_providers {
    apko = { source = "chainguard-dev/apko" }
  }
}

variable "target_repository" {}

variable "content" {}

data "apko_config" "sandbox" {
  config_contents = jsonencode({
    contents = {
      packages = ["busybox", "bash", "helm", "kubectl", "jq"]
    }
    cmd = "bash -eux -o pipefail -c 'source /imagetest/foo.sh'"
  })
}

resource "apko_build" "sandbox" {
  repo   = var.target_repository
  config = data.apko_config.sandbox.config
}

output "test" {
  value = [{
    name  = "helm test"
    image = apko_build.sandbox.image_ref
    content = [{
      source = var.content
    }]
  }]
}
