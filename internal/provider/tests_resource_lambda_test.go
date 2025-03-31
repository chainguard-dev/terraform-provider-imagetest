//go:build lambda
// +build lambda

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource_Lambda(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{repo: repo}),
		},
		Steps: []resource.TestStep{{Config: `resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "lambda"
  
  tests = [{
	name    = "test"
	// TODO: this needs to be an actual image with a Lambda function in it, not just the base image.
	image   = "public.ecr.aws/lambda/python:3.13@sha256:3439857092837402879d7892d2ce7cb2290c7ab29644db9ea51cf1cf20d95be3"
  }]
}`}},
	})
}
