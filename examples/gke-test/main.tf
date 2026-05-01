terraform {
  required_providers {
    imagetest = {
      source = "chainguard-dev/imagetest"
    }
  }
}

# Configure the provider to use a public registry
# ttl.sh is a public registry with 24hr retention (no auth required)
# This matches AKS/EKS test pattern
provider "imagetest" {
  repo = "ttl.sh/imagetest-gke"
}

resource "imagetest_tests" "gke_basic" {
  name   = "gke-basic-test"
  driver = "gke"

  drivers = {
    gke = {
      project = "<YOUR_GCP_PROJECT_ID>"
      region  = "us-central1"

      # Use a unique cluster name to avoid conflicts
      # cluster_name = "imagetest-gke-test-final"  # Let GKE auto-generate

      # Optional: customize cluster
      # node_count    = 2
      # machine_type  = "n1-standard-4"
      # disk_size_gb  = 150
      # disk_type     = "pd-ssd"

      # Optional: add custom labels
      # tags = {
      #   environment = "test"
      #   team        = "platform"
      # }
    }
  }

  # NOTE: imagetest requires digest references (@sha256:...), not tags (:latest)
  # To get the current digest:
  #   crane digest cgr.dev/chainguard/nginx:latest
  # Using public images - provider will modify and push to your repo
  images = {
    nginx = "cgr.dev/chainguard/nginx:latest@sha256:25f70f9f4d82518a547ec16a02d7cbd81a8bb0cc3278259789b750f834803798"
  }

  tests = [
    {
      name = "smoke-test"
      # Test sandbox image - provides kubectl and other tools
      image = "cgr.dev/chainguard/kubectl:latest-dev@sha256:9d3f6aa5c7741d84ca6a82935df987162f7c53692f98c48624c1871f03c40f8b"
      cmd   = <<-EOF
        set -ex

        apk add jq

        # Verify kubectl works
        kubectl version --client
        kubectl get nodes

        # Test deploying the image
        kubectl run nginx-test --image=$(echo "$IMAGES" | jq -r '.nginx.ref') --dry-run=client -o yaml

        echo "✓ GKE cluster is working!"
      EOF
      on_failure = [
        "kubectl describe pod $POD_NAME -n $POD_NAMESPACE",
        "kubectl get events -n $POD_NAMESPACE --sort-by='.lastTimestamp'",
        "kubectl get pods -n $POD_NAMESPACE -o wide",
        "kubectl get nodes -o wide",
      ]
    }
  ]

  # GKE cluster creation takes ~10-15 minutes
  timeout = "45m"
}

output "test_results" {
  value = imagetest_tests.gke_basic
}
