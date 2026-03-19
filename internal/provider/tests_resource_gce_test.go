//go:build gce

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
	//go:embed testdata/TestAccTestsConfigs/driver-gce-basic.tf
	configDriverGCEBasic string
	//go:embed testdata/TestAccTestsConfigs/driver-gce-driver-commands-fail.tf
	configDriverGCEDriverCommandsFail string
	//go:embed testdata/TestAccTestsConfigs/driver-gce-test-commands-fail.tf
	configDriverGCETestCommandsFail string
)

var gceTests = map[string][]resource.TestStep{
	"basic": {{
		Config: configDriverGCEBasic,
	}},
	"driver-commands-fail": {{
		Config:      configDriverGCEDriverCommandsFail,
		ExpectError: regexp.MustCompile("Process exited with status 1"),
	}},
	"test-commands-fail": {{
		Config:      configDriverGCETestCommandsFail,
		ExpectError: regexp.MustCompile("container exited with code 1"),
	}},
}

func TestAccTestDriverGCE(t *testing.T) {
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

	for name, steps := range gceTests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps:                    steps,
			})
		})
	}
}
