terraform {
  required_providers {
    imagetest = { source = "chainguard-dev/imagetest" }
  }
}

# Run a simple test that runs a container with docker and expects a non-zero
# exit code
resource "imagetest_test_docker_run" "basic" {
  name  = "basic"
  image = "hello-world"
}
