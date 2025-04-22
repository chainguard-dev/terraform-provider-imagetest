//go:build lambda
// +build lambda

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource_Lambda(t *testing.T) {
	ref := os.Getenv("IMAGETEST_LAMBDA_TEST_IMAGE_REF")
	if ref == "" {
		t.Fatal("IMAGETEST_LAMBDA_TEST_IMAGE_REF must be set")
	}
	executionRole := os.Getenv("IMAGETEST_LAMBDA_TEST_EXECUTION_ROLE")
	if executionRole == "" {
		t.Fatal("IMAGETEST_LAMBDA_TEST_EXECUTION_ROLE must be set")
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{}),
		},
		Steps: []resource.TestStep{{Config: fmt.Sprintf(`resource "imagetest_tests_lambda" "foo" {
  execution_role = %q
  image_ref      = %q
}`, executionRole, ref)}},
	})
}
