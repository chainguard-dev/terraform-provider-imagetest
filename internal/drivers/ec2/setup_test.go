package ec2

import (
	"context"
	"log/slog"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
)

func init() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
}

func TestSetup(t *testing.T) {
	x := &Driver{
		AMI: "ami-05f991c49d264708f",
		Proc: Proc{
			Architecture: types.ArchitectureTypeX8664,
			VCPUs:        2,
		},
	}

	// Init a default AWS config
	cfg, err := config.LoadDefaultConfig(t.Context())
	assert.NoError(t, err)

	// Init an EC2 client, assign it to the driver instance
	x.client = ec2.NewFromConfig(cfg)

	err = x.Setup(context.Background())
	assert.NoError(t, err)

	// typ, err := selectInstanceType(t.Context(), x)
	// assert.NoError(t, err)
	// slog.Debug("selected instance type", "instance_type", typ)
}

func TestParseMemory(t *testing.T) {
	// NOTE: See the logic around unit conversion in the comments above
	// `parseMemoryCapacity`. Some of the pairings below might look unintuitive
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
