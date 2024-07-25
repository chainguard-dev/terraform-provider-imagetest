package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccFeatureResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and read testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_docker" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "Ordering"
  description = "Test the step ordering"
  harness = imagetest_harness_docker.test
  before = [
    {
      name = "1"
      cmd = "echo first >> /tmp/feature_test"
    },
  ]
  after = [
    {
      name = "3"
      cmd = "echo third >> /tmp/feature_test"
    },
    {
      name = "assert"
      cmd = <<EOF
        cat /tmp/feature_test
        echo -e "first\nsecond\nthird" | diff - /tmp/feature_test > /dev/null
      EOF
    },
  ]
  steps = [
    {
      name = "2"
      cmd = "echo second >> /tmp/feature_test"
    },
  ]
}
        `,
			},
		},
	})
}

func TestAccFeatureResourceRetry(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and read testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_docker" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "Retry"
  description = "Test the step ordering"
  harness = imagetest_harness_docker.test
  steps = [
    {
      # NOTE: This technically will succeed for > n_attempts, but since the
      # actual n_attempts is handled by wait.Backoff(), we don't worry about it
      name = "ensure we retry"
      cmd = <<EOF
        file=/tmp/feature_test
        if [ ! -f $file ]; then
          echo 0 > $file
        fi

        echo $(( $(cat $file) + 1 )) > $file

        if [ $(cat $file) -lt 3 ]; then
          exit 1
        fi
      EOF
      retry = {
        attempts = 3
        delay = "0s"
      }
    },
    {
      name = "assert"
      cmd = <<EOF
        if [ $(cat /tmp/feature_test) -ne 3 ]; then
          echo "Expected 3 retries, got $(cat /tmp/feature_test)"
          exit 1
        fi
      EOF
    },
  ]
}
        `,
			},
		},
	})
}

// TestAccFeatureResourceUpdate tests that this provider works with Update()
// requests as well. This also hits the base_harness path, where all the
// harness update logic is located.
func TestAccFeatureResourceUpdate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Destroy:            false,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_docker" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "update"
  description = "Test whether creates work"
  harness = imagetest_harness_docker.test
  steps = [
    {
      name = "something"
      cmd = "echo do something"
    },
  ]
}
`,
			},
			// Update testing
			{
				ExpectNonEmptyPlan: true,
				Destroy:            false,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_docker" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "update"
  description = "Test whether updates work"
  harness = imagetest_harness_docker.test
  steps = [
    {
      name = "something"
      cmd = "echo do another something"
    },
  ]
}
`,
			},
		},
	})
}
