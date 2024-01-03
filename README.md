# Terraform Provider Image Test

🚨 **This is a work in progress** 🚨

A terraform provider for authoring and executing tests using terraform primitives. Designed to work in conjunction with the [Chainguard Images](https://github.com/chainguard-dev/images) project.

## Usage

```hcl
# Create a test harness
resource "imagetest_harness_k3s" "this" {}
resource "imagetest_harness_teardown" "k3s" { harness = imagetest_harness_k3s.this.id }

# Run features against the harness in a preconfigured ephemeral sandbox
resource "imagetest_feature" "k3s" {
  name        = "My great feature"
  description = "A simple feature that tests something against an ephemeral k3s cluster."

  harness = imagetest_harness_k3s.this.id

  # Run a series of steps in an ephemeral sandbox. 
  steps = [
    {
      name = "Do things with the cluster"
      cmd  = <<EOF
        kubectl get po -A
        kubectl run nginx --image=cgr.dev/chainguard/nginx:latest
      EOF
    },
  ]

  # Define labels to filter which features to evaluate at runtime
  labels = {
    size = "small"
    type = "k8s"
  }
}
```

See [examples](./examples) for more usages.
