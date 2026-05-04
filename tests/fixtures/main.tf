terraform {
  required_providers {
    apko      = { source = "chainguard-dev/apko" }
    imagetest = { source = "chainguard-dev/imagetest" }
    oci       = { source = "chainguard-dev/oci" }
    random    = { source = "hashicorp/random" }
  }

  backend "inmem" {}
}

variable "target_repository" {
  description = <<-EOT
    Writable registry path used both by the imagetest provider itself (to push
    its entrypoint/harness bundle) and by fixtures that push sandbox images
    via bash-sandbox + docker-in-docker (busybox, go, maven, python, ruby,
    curl, redis). Set to a localhost:5000 sidecar registry in CI, or
    ttl.sh/<unique> for ad-hoc local runs.
  EOT
  type        = string
}

provider "imagetest" {
  repo = var.target_repository
}

variable "test_repository" {
  description = <<-EOT
    Repository root used by curl's test to reference a sibling image
    (e.g. <test_repository>/python:latest-dev). Defaults to the public
    cgr.dev/chainguard root.
  EOT
  type        = string
  default     = "cgr.dev/chainguard"
}

provider "apko" {
  extra_repositories = ["https://packages.wolfi.dev/os"]
  extra_keyring      = ["https://packages.wolfi.dev/os/wolfi-signing.rsa.pub"]
  default_archs      = ["x86_64", "aarch64"]
}

locals {
  # Tag-pinned refs to the currently-published images on cgr.dev. The
  # oci_exec_test data source used by some fixtures (wolfi-base, nginx)
  # requires a digest-pinned ref, so each tag is resolved via `crane digest`
  # in the external data source below.
  image_tags = {
    jre        = "cgr.dev/chainguard/jre:latest"
    nginx      = "cgr.dev/chainguard/nginx:latest"
    wolfi-base = "cgr.dev/chainguard/wolfi-base:latest"
    busybox    = "cgr.dev/chainguard/busybox:latest"
    go         = "cgr.dev/chainguard/go:latest"
    maven      = "cgr.dev/chainguard/maven:latest"
    python     = "cgr.dev/chainguard/python:latest"
    ruby       = "cgr.dev/chainguard/ruby:latest"
    curl       = "cgr.dev/chainguard/curl:latest"
    redis      = "cgr.dev/chainguard/redis:latest"
  }
}

# Requires `crane` on PATH (github.com/google/go-containerregistry/cmd/crane).
# CI installs it via `go install`; locally, ensure crane is installed.
data "external" "resolved" {
  for_each = local.image_tags
  program = ["sh", "-c",
    "printf '{\"ref\":\"%s@%s\"}\\n' \"$(echo '${each.value}' | cut -d: -f1)\" \"$(crane digest '${each.value}')\""
  ]
}

locals {
  resolved = { for k, v in data.external.resolved : k => v.result.ref }
}

module "jre" {
  source = "./images/jre/tests"
  digest = local.resolved["jre"]
}

module "nginx" {
  source = "./images/nginx/tests"
  digest = local.resolved["nginx"]
}

module "wolfi-base" {
  source = "./images/wolfi-base/tests"
  digest = local.resolved["wolfi-base"]
}

module "busybox" {
  source            = "./images/busybox/tests"
  digest            = local.resolved["busybox"]
  target_repository = var.target_repository
}

module "go" {
  source            = "./images/go/tests"
  digest            = local.resolved["go"]
  target_repository = var.target_repository
  image_version     = "latest"
}

module "maven" {
  source            = "./images/maven/tests"
  digest            = local.resolved["maven"]
  target_repository = var.target_repository
}

module "python" {
  source            = "./images/python/tests"
  digest            = local.resolved["python"]
  target_repository = var.target_repository
}

module "ruby" {
  source            = "./images/ruby/tests"
  digest            = local.resolved["ruby"]
  target_repository = var.target_repository
}

module "curl" {
  source            = "./images/curl/tests"
  digest            = local.resolved["curl"]
  target_repository = var.target_repository
  test_repository   = var.test_repository
  image_version     = "latest"
}

module "redis" {
  source            = "./images/redis/tests"
  digest            = local.resolved["redis"]
  target_repository = var.target_repository
}
