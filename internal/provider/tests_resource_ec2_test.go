//go:build ec2
// +build ec2

package provider

/*
tests_resource_ec2_test.go tests the EC2 driver.

To test locally, here's the magic incantation:
```
IMAGETEST_ENTRYPOINT_REF=$(KO_DOCKER_REPO=ttl.sh/imagetest ko build ./cmd/entrypoint) \
TF_ACC=1 \
  go test \
    -tags ec2 ./internal/provider \
    -run '^TestAccTestDriverEC2$' \
    -count 1 \
    -v
```
*/

import (
	_ "embed"
	"log/slog"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

var (
	//go:embed testdata/TestAccTestsConfigs/driver-ec2-basic.tf
	configDriverEC2Basic string
	//go:embed testdata/TestAccTestsConfigs/driver-ec2-driver-commands-fail.tf
	configDriverEC2DriverCommandsFail string
	//go:embed testdata/TestAccTestsConfigs/driver-ec2-test-commands-fail.tf
	configDriverEC2TestCommandsFail string
	//go:embed testdata/TestAccTestsConfigs/driver-ec2-volume-mount.tf
	configDriverEC2VolumeMount string
)

func TestAccTestDriverEC2(t *testing.T) {
	const registryURI = "cgr.dev/chainguard-eng"

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})))

	// Construct the provider server.
	pserver := providerserver.NewProtocol6WithError(
		&ImageTestProvider{
			repo: registryURI,
		},
	)

	// Construct the provider factory map.
	type ProviderFactoryFn = func() (tfprotov6.ProviderServer, error)
	factories := map[string]ProviderFactoryFn{
		"imagetest": pserver,
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			// Verifies a simple 'exit 0' passes.
			{
				Config: configDriverEC2Basic,
			},
			// Verifies a failure which occurs in the 'drivers' object commands fails
			// the run.
			{
				Config:      configDriverEC2DriverCommandsFail,
				ExpectError: regexp.MustCompile("Process exited with status 1"),
			},
			// Verifies a test failure is properly caught as a failure.
			{
				Config:      configDriverEC2TestCommandsFail,
				ExpectError: regexp.MustCompile("container exited with code: 1"),
			},
			// Verifies a volume mount is successful.
			{
				Config: configDriverEC2VolumeMount,
			},
		},
	})
}
