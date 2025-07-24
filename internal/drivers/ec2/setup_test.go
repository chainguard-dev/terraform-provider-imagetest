package ec2

import (
	"log/slog"
	"os"
	"os/signal"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
}

func TestSetup(t *testing.T) {
	ctx, cancel := signal.NotifyContext(t.Context(), os.Kill, os.Interrupt)
	defer cancel()
	// Init a default AWS config.
	cfg, err := config.LoadDefaultConfig(ctx)
	assert.NoError(t, err)
	// Init an EC2 client, assign it to the driver instance.
	client := ec2.NewFromConfig(cfg)
	// Construct the driver.
	driver, err := NewDriver(client)
	require.NoError(t, err)
	driver.AMI = "ami-08b674058d6b8d3f6"
	driver.Proc = Proc{
		Architecture: types.ArchitectureTypeArm64,
		VCPUs:        2,
	}
	driver.GPU.Kind = GPUKindH200
	driver.Commands.User = "ubuntu"
	driver.Commands.Shell = ssh.ShellBash
	driver.Commands.Commands = []string{
		"sudo docker run hello-world",
	}
	require.NoError(t, driver.Setup(ctx))
	_, err = driver.Run(ctx, name.Digest{})
	assert.NoError(t, err)
	require.NoError(t, driver.Teardown(ctx))
}

func TestParseMemory(t *testing.T) {
	// NOTE: See the logic around unit conversion in the comments above
	// 'parseMemoryCapacity'. Some of the pairings below might look unintuitive
	// at first glance.
	//
	// Standard descending unit of measurement tests
	testParseMemory(t, "200", 190734)    // 200GB   == 190600 MiB
	testParseMemory(t, "200GB", 190734)  // 200GB   == 190600 MiB
	testParseMemory(t, "200MB", 190)     // 200MB   == 190    MiB
	testParseMemory(t, "2000KB", 1)      // 2000KB  == 1      MiB
	testParseMemory(t, "200GiB", 204800) // 200GiB  == 204800 MiB
	testParseMemory(t, "200MiB", 200)    // 200MiB  == 200    MiB
	testParseMemory(t, "2000KiB", 1)     // 2000KiB  == 1     MiB
	// MeMeCasE test (parse should be case-insensitive)
	testParseMemory(t, "200", 190734)    // 200GB   == 190600 MiB
	testParseMemory(t, "200gB", 190734)  // 200GB   == 190600 MiB
	testParseMemory(t, "200Mb", 190)     // 200MB   == 190    MiB
	testParseMemory(t, "2000kB", 1)      // 2000KB  == 1      MiB
	testParseMemory(t, "200gIb", 204800) // 200GiB  == 204800 MiB
	testParseMemory(t, "200miB", 200)    // 200MiB  == 200    MiB
	testParseMemory(t, "2000kiB", 1)     // 2000KiB  == 1     MiB
	// Random spacing tests
	testParseMemory(t, " 200", 190734)    // 200GB   == 190600 MiB
	testParseMemory(t, "200 GB", 190734)  // 200GB   == 190600 MiB
	testParseMemory(t, "200MB ", 190)     // 200MB   == 190    MiB
	testParseMemory(t, "2000 KB", 1)      // 2000KB  == 1      MiB
	testParseMemory(t, " 200GiB", 204800) // 200GiB  == 204800 MiB
	testParseMemory(t, "200 MiB", 200)    // 200MiB  == 200    MiB
	testParseMemory(t, " 2000 KiB", 1)    // 2000KiB  == 1     MiB
}

func testParseMemory(t *testing.T, input string, expect uint32) {
	t.Helper()
	n := parseMemoryCapacity(t.Context(), input)
	assert.Equal(t, expect, n, "expected [%d] got [%d]", expect, n)
}
