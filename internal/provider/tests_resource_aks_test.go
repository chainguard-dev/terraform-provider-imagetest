//go:build aks

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource_AKS(t *testing.T) {
	resourceGroup := os.Getenv("IMAGETEST_AKS_RESOURCE_GROUP")
	subscriptionID := os.Getenv("IMAGETEST_AKS_SUBSCRIPTION_ID")
	nodeResourceGroup := resourceGroup + "-node-" + uuid.New().String()

	repo := "ttl.sh/imagetest" // TODO: Don't push to ttl.sh

	tf := fmt.Sprintf(`
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "aks"

  drivers = {
    aks = {
      resource_group = %q
      subscription_id = %q
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
      cmd     = "echo success"
    }
  ]

  // Cluster provisioning usually takes about 5 minutes.
  timeout = "30m"
}
`, resourceGroup, subscriptionID)

	// Test with storage configuration and custom tags
	tfWithStorage := fmt.Sprintf(`
resource "imagetest_tests" "foo_with_storage" {
  name   = "foo-with-storage"
  driver = "aks"

  drivers = {
    aks = {
      resource_group = %q
      node_resource_group = %q
      subscription_id = %q
      node_vm_size = "Standard_DS2_v2"
      node_count = 2
      node_disk_size = 30
      node_disk_type = "Ephemeral"
      tags = {
        "team"        = "platform"
        "environment" = "test"
      }
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
      cmd     = "echo success"
    }
  ]

  // Cluster provisioning usually takes about 5 minutes.
  timeout = "30m"
}
`, resourceGroup, nodeResourceGroup, subscriptionID)

	readerRoleGuid := "acdd72a7-3385-48ef-bd42-f606fba81ae7"
	scope := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s",
		subscriptionID,
		resourceGroup,
	)
	role := fmt.Sprintf(
		"/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s",
		subscriptionID,
		readerRoleGuid,
	)
	tfWithPodIdentityAssociation := fmt.Sprintf(`
resource "imagetest_tests" "foo_with_pod_identity" {
  name   = "foo"
  driver = "aks"

  drivers = {
    aks = {
      resource_group = %q
      subscription_id = %q

      // "Owner" role is required (e.g. RG scoped) in order to
      // attach the ACR.
      attached_acrs = [
        {
          name = "imageTestTempACR"
          create_if_missing = true
        }
      ]

      pod_identity_associations = [
        {
          service_account_name = "default",
          namespace = "default",
          roles: [
            {
              scope = %q
              role_definition_id = %q
            },
          ],
        },
      ]

      cluster_identity_associations = [
        {
          identity_name = "kubeletidentity"
          role_assignments = [
            {
              scope = %q
              role_definition_id = %q
            }
          ]
        }
      ]
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
      cmd     = "echo success"
    }
  ]

  // Cluster provisioning usually takes about 5 minutes.
  timeout = "30m"
}
`, resourceGroup, subscriptionID, scope, role, scope, role)

	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
				repo: repo,
			}),
		},
		Steps: []resource.TestStep{
			{Config: tf},
			{Config: tfWithStorage},
			{Config: tfWithPodIdentityAssociation},
		},
	})
}
