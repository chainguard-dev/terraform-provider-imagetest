package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHarnessK3sResource(t *testing.T) {
	t.Parallel()

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
    {
      name = "step 1"
      cmd = "cat /root/.profile"
    },
    {
      name = "Access cluster using alias"
      cmd = "k get po -A"
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
		"sandbox config with bundler": {
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
      "test": "cgr.dev/chainguard/wolfi-base:latest",
    }
    packages = ["crane"]
    layers = [{ source = path.module, target = "/src/bar/" }]
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
    {
      name = "use package"
      cmd = "crane digest $test"
    },
    {
        name = "check layer"
        cmd = "cat /src/bar/harness_k3s_resource_test.go"
    },
  ]
}
          `,
			},
		},
		"sandbox config with appender": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  sandbox = {
    image = "cgr.dev/chainguard/wolfi-base:latest"
    layers = [{ source = path.module, target = "/src/bar/" }]
  }
}

resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that we can spin up a k3s cluster and run some steps"
  harness = imagetest_harness_k3s.test
  steps = [
    {
        name = "check base"
        cmd = "! kubectl --help" # should fail
    },
    {
        name = "check layer"
        cmd = "cat /src/bar/harness_k3s_resource_test.go"
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

  registries = {
    "foo" = {
      auth = {
        username = "testuser"
        password = "testpass"
      }
    }
  }

  hooks = {
    post_start = [
      "echo 'post' > /tmp/hi",
    ]
  }

  provisioner "local-exec" {
    command = <<EOF
docker exec ${self.id} sh -c "cat /tmp/hi"
docker exec ${self.id} sh -c "kubectl get secret -n kube-system imagetest-registry-auth -o json"
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
		"with additional networks": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  networks = { "bridge" = { name = "bridge" } }
}

resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that we can attach existing networks"
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
		"with local registry": {
			// Create testing
			{
				ExpectNonEmptyPlan: true,
				Config: `
data "imagetest_inventory" "this" {}

resource "terraform_data" "registry_up" {
  provisioner "local-exec" {
    command = "docker run --name it-test-registry -d -p 12344:5000 registry:2"
  }
}

resource "imagetest_harness_k3s" "test" {
  name = "test"
  inventory = data.imagetest_inventory.this
  depends_on = [terraform_data.registry_up]
}

resource "imagetest_feature" "test" {
  name = "Simple k3s based test"
  description = "Test that we can attach existing networks"
  harness = imagetest_harness_k3s.test
  steps = [
    {
      name = "Access cluster"
      cmd = <<EOF
        kubectl get po -A

        apk add curl jq
        curl -v http://host.docker.internal:12344/v2/ | jq '.'
      EOF
    },
  ]
}

resource "terraform_data" "registry_down" {
  provisioner "local-exec" {
    command = "docker rm -f it-test-registry"
  }
  depends_on = [imagetest_feature.test]
}
          `,
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testProviderWithRegistry(t, context.Background()),
				Steps:                    tc,
			})
		})
	}
}
