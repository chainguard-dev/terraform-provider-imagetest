package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHarnessK3sResource(t *testing.T) {
	testCases := map[string][]resource.TestStep{
		"happy path": {
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
		"with working directory": {
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
		"timeout": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				ExpectError:        regexp.MustCompile(`.*context\s+deadline.*`),
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  timeouts = {
    create = "0s"
  }
}

resource "imagetest_feature" "test" {
  name = "Dummy"
  description = "Should never get here"
  harness = imagetest_harness_k3s.test
  steps = []
}
          `,
			},
		},
		"sandbox config": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  sandbox = {
    envs = {
      "test": "value",
    }
  }
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
		"with memory configuration": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  sandbox = {
    envs = {
      "test": "value",
    }
  }

  resources = {
    cpu = {
      request = "1"
    }
    memory = {
      request = "524288000" # 500Mi
      limit   = "524288001"
    }
  }

  provisioner "local-exec" {
    command = <<EOF
docker inspect ${self.id} | jq '.[0].HostConfig.NanoCpus' | grep 1
docker inspect ${self.id} | jq '.[0].HostConfig.MemoryReservation' | grep 524288000
docker inspect ${self.id} | jq '.[0].HostConfig.Memory' | grep 524288001
      EOF
  }
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
		"with posthook": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this

  hooks = {
    post_start = [
      "echo 'post' > /tmp/hi",
    ]
  }

  provisioner "local-exec" {
    command = <<EOF
docker exec ${self.id} sh -c "cat /tmp/hi"
      EOF
  }
}

resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that post hooks work"
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
		"with kubelet config YAML": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  kubelet_config = <<EOconfig
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
registryPullQPS: 10
registryBurst: 20
EOconfig
}

resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that one can apply a custom KubeletConfig"
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
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps:                    tc,
			})
		})
	}
}
