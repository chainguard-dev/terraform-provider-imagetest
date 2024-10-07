# Skip all tests
provider "imagetest" {
  test_execution = {
    skip_all = true
  }
}

# Skip all tests that are labeled "flaky"
provider "imagetest" {
  test_execution = {
    exclude_by_label = {
      "flaky" = "true"
    }
  }
}

# Only run K8s tests that are small, while skipping flaky tests
provider "imagetest" {
  test_execution = {
    include_by_label = {
      "type" = "k8s"
      "size" = "small"
    }
    exclude_by_label = {
      "flaky" = "true"
    }
  }
}
