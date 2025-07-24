package ec2

import (
	"log/slog"
	"os"
	"os/signal"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
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
