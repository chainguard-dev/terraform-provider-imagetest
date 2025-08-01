//go:build ec2
// +build ec2

package provider

// tests_resource_ec2_test.go tests the EC2 driver.
//
// To test locally, use the 'make' target ('make ec2acc') and refer to
// '.github/scripts/acc-test-driver-ec2.sh' for more details.

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
	//go:embed testdata/TestAccTestsConfigs/driver-ec2-gpu-mount.tf
	configDriverEC2GPUMount string
)

var tests = map[string][]resource.TestStep{
	// Verifies a simple 'exit 0' passes.
	"driver-ec2-basic": {{
		Config: configDriverEC2Basic,
	}},
	// Verifies a failure which occurs in the 'drivers' object commands fails
	// the run.
	"driver-ec2-driver-commands-fail": {{
		Config:      configDriverEC2DriverCommandsFail,
		ExpectError: regexp.MustCompile("Process exited with status 1"),
	}},
	// Verifies a test failure is properly caught as a failure.
	"driver-ec2-test-commands-fail": {{
		Config:      configDriverEC2TestCommandsFail,
		ExpectError: regexp.MustCompile("container exited with code: 1"),
	}},
	// Verifies a volume mount is successful.
	"driver-ec2-with-volume-mount": {{
		Config: configDriverEC2VolumeMount,
	}},
	// Verifies a GPU mount is successful.
	"driver-ec2-with-gpu": {{
		Config: configDriverEC2GPUMount,
	}},
}

func TestAccTestDriverEC2(t *testing.T) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	// Set a default registry URI if one was not provided via env vars.
	const defaultRegistryURI = "ttl.sh/terraform-provider-imagetest"
	var registryURI string
	var ok bool
	if registryURI, ok = os.LookupEnv("IMAGETEST_REGISTRY"); ok {
		slog.Info(
			"using registry from environment ('IMAGETEST_REGISTRY')",
			"registry", registryURI,
		)
	} else {
		registryURI = defaultRegistryURI
		slog.Info(
			"using default registry ('IMAGETEST_REGISTRY' not set)",
			"registry", registryURI,
		)
	}

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

	for name, steps := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps:                    steps,
			})
		})
	}
}
