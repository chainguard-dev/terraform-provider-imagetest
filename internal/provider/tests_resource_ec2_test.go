//go:build ec2

package provider

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
	//go:embed testdata/TestAccTestsConfigs/driver-ec2-iam-auto.tf
	configDriverEC2IAMAuto string
)

var ec2Tests = map[string][]resource.TestStep{
	"basic": {{
		Config: configDriverEC2Basic,
	}},
	"driver-commands-fail": {{
		Config:      configDriverEC2DriverCommandsFail,
		ExpectError: regexp.MustCompile("Process exited with status 1"),
	}},
	"test-commands-fail": {{
		Config:      configDriverEC2TestCommandsFail,
		ExpectError: regexp.MustCompile("container exited with code 1"),
	}},
	"volume-mount": {{
		Config: configDriverEC2VolumeMount,
	}},
	"iam-auto": {{
		Config: configDriverEC2IAMAuto,
	}},
}

func TestAccTestDriverEC2(t *testing.T) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	registryURI := os.Getenv("IMAGETEST_REGISTRY")
	if registryURI == "" {
		registryURI = "ttl.sh/terraform-provider-imagetest"
		slog.Info("using default registry", "registry", registryURI)
	} else {
		slog.Info("using registry from environment", "registry", registryURI)
	}

	pserver := providerserver.NewProtocol6WithError(&ImageTestProvider{repo: registryURI})
	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"imagetest": pserver,
	}

	for name, steps := range ec2Tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps:                    steps,
			})
		})
	}
}
