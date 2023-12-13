# Terraform Provider Image Test

🚨 **This is a work in progress** 🚨

A terraform provider for authoring and executing tests using terraform primitives. Designed to work in conjunction with the [Chainguard Images](https://github.com/chainguard-dev/images) project.

## Usage

This provides several resources for authoring and executing tests:

- `imagetest_environment`: Define ephemeral environments to execute tests against
- `imagetest_feature`: Author features to test
- `imagetest_harness_*`: Define reusable test harnesses

```hcl
# Define features to test against environments
resource "imagetest_feature" "footure" {
  name        = "footure"
  description = "My great footure"

  setup {
    cmd = "echo 'setup'" # do some feature specific setup
  }

  teardown {
    cmd = "echo 'teardown'" # some feature specific teardown
  }

  assert {
    cmd = "echo 'first assertion'" # run assertions that pass or fail
  }

  assert {
    cmd = "kubectl get po -A" # assertions are environment configuration independent
  }
}

# Define reusable environment test harnesses
resource "imagetest_harness_k3s" "simple" {}
resource "imagetest_harness_teardown" "simple" { harness = imagetest_harness_k3s.simple.id }

# Define testing environments
resource "imagetest_environment" "foo" {
  harness = imagetest_harness_k3s.simple.id

  test {
    features = [imagetest_feature.footure.id]
  }
}

# Retrieve test results as machine readable test reports
output "foo_report" {
  values = imagetest_environment.foo.report
}
```
