package ec2

import (
	"context"
	"fmt"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/google/go-containerregistry/pkg/name"
)

func (d *Driver) Run(ctx context.Context, name name.Reference) (*drivers.RunResult, error) {
	log := clog.FromContext(ctx)
	// Convert the ED25519 private key to an 'ssh.Signer'.
	signer, err := d.instance.Keys.Private.ToSSH()
	if err != nil {
		return nil, err // No annotation required.
	}
	log.Debug("ed25519 private key converted to OpenSSH format")
	// Establish the SSH connection.
	log.Info(
		"connecting to EC2 instance via SSH",
		"user", d.Commands.User,
		"host", d.net.ElasticIP,
		"port", portSSH,
	)
	client, err := ssh.Connect(
		d.net.ElasticIP,
		portSSH,
		d.Commands.User,
		signer,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to instance via SSH: %w", err)
	}
	log.Info("SSH connection to EC2 instance is successful")
	// Execute all user-provided commands.
	err = ssh.ExecIn(
		client,
		d.Commands.Shell,
		os.Stdout,
		os.Stderr,
		d.Commands.Commands...,
	)
	if err != nil {
		return nil, fmt.Errorf("encountered error in command execution: %w", err)
	}
	log.Info("command successfully executed against EC2 instance")
	return nil, nil
}
