package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHarnessK3sResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that we can spin up a k3s cluster and run some steps"
  harness = imagetest_harness_k3s.test
  steps = [
    {
      name = "Access cluster"
      cmd = "kubectl get po -A"
    },
  ]
}
          `,
			},
		},
	})
}

func TestAccHarnessK3sResourceWithWorkingDirectory(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
}

resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that we can spin up a k3s cluster and run some steps"
  harness = imagetest_harness_k3s.test
  steps = [
    {
      name = "Create /src dir"
      cmd  = "mkdir -p /src"
    },
    {
      name    = "Create simple test file"
      cmd     = <<EOM
cat <<EOF > testcm.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config-map
data:
  config.yaml: |
    test-key1: test-value1
    test-key2: test-value2
EOF
EOM
      workdir = "/src"
    },
    {
      name    = "Access cluster"
      cmd     = "kubectl apply -f testcm.yaml"
      workdir = "/src"
    },
  ]
}
          `,
			},
		},
	})
}
