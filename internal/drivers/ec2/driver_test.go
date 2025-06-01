package ec2

import (
	"context"
	"log"
	"log/slog"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestDrive(t *testing.T) {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	slogger := slog.New(handler)
	slog.SetDefault(slogger)
	slog.SetLogLoggerLevel(slog.LevelDebug)
	log.SetFlags(log.Lshortfile)

	ctx := context.Background()

	// Init a default AWS config
	cfg, err := config.LoadDefaultConfig(ctx)
	assert.NoError(t, err)

	// Init an EC2 client, assign it to the driver instance
	client := ec2.NewFromConfig(cfg)
	assert.NotNil(t, client)

	// Init the driver
	d := &Driver{
		Proc: Proc{
			VCPUs: 4,
			// ThreadsPerCore: 2,
		},
		Memory: Memory{
			Capacity: "100MB",
		},
		client: client,
	}

	// Ju-lee, do the thing!
	err = d.Setup(context.Background())
	assert.NoError(t, err)
}
