package provider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
)

// testAccProtoV6ProviderFactories are used to instantiate a provider during
// acceptance testing. The factory function will be invoked for every Terraform
// CLI command executed to create a provider server to which the CLI can
// reattach.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	// "imagetest": providerserver.NewProtocol6WithError(New("test")()),
	"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{}),
}

func testAccPreCheck(t *testing.T) {
	// You can add code here to run prior to any test case execution, for example assertions
	// about the appropriate environment variables being set are common to see in a pre-check
	// function.
}

func testProviderWithRegistry(t *testing.T, ctx context.Context) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
			repo: testRegistry(t, ctx),
		}),
	}
}

func testRegistry(t *testing.T, ctx context.Context) string {
	cli, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}

	resp, err := cli.Start(ctx, &docker.Request{
		Ref: name.MustParseReference("registry:2"),
		PortBindings: nat.PortMap{
			nat.Port("5000"): []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: "",
				},
			},
		},
		HealthCheck: &v1.HealthcheckConfig{
			Test:        []string{"CMD", "/bin/sh", "-c", "wget -O- -q  http://localhost:5000/v2/"},
			Interval:    1 * time.Second,
			StartPeriod: 1 * time.Second,
			Timeout:     1 * time.Minute,
		},
		Labels: map[string]string{
			"dev.chainguard.imagetest": "true",
		},
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := cli.Remove(ctx, resp); err != nil {
			t.Fatal(err)
		}
	})

	eport := resp.NetworkSettings.Ports["5000/tcp"][0].HostPort
	return fmt.Sprintf("localhost:%s/foo", eport)
}
