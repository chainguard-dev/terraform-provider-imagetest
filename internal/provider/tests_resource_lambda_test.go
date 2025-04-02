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
	repo := "452336408843.dkr.ecr.us-west-2.amazonaws.com/jason-lambda-python"
	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{repo: repo}),
		},
		Steps: []resource.TestStep{{Config: `resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "lambda"

  images = {}
  
  tests = [{
    name = "foo"
	// base image for test sandbox (tools, jq, etc)
    image = "452336408843.dkr.ecr.us-west-2.amazonaws.com/jason-lambda-python:test@sha256:07a99c500939444fc8a821e508fd84d8f16f494638c8a75cd2fb1a90cfd29ab9"
  }]
}`}},
	})
}
