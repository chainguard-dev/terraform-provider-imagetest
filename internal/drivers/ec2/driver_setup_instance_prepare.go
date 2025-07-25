package ec2

import (
	"bytes"
	"context"
	"fmt"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

var ErrProvisionInstance = fmt.Errorf("failed to perform standard software setup steps on provisioned EC2 instance")

// prepareInstance performs the in-host (via SSH) configuration required for
// image tests.
func (d *Driver) prepareInstance(ctx context.Context, inst InstanceDeployment, net NetworkDeployment) error {
	log := clog.FromContext(ctx)

	// Marshal the private key to the OpenSSH format.
	privKey, err := inst.Keys.Private.ToSSH()
	if err != nil {
		return err // No wrapping required.
	}
	log.Debug("ED255219 private key marshal to 'ssh.Signer' is successful")

	// Establish an SSH connection to the instance.
	conn, err := ssh.Connect(net.ElasticIP, portSSH, d.Exec.User, privKey)
	if err != nil {
		return err // No wrapping required.
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Warn("failed to close SSH connection", "error", err)
		}
	}()
	log.Debug("instance SSH connection is successful")

	// Detonate all boilerplate setup commands on the remote host.
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	err = ssh.ExecIn(
		conn,
		ssh.ShellBash,
		stdout,
		stderr,
		cmdSetDefault...,
	)
	if err != nil {
		log.Error(
			"SSH command execution failed",
			"error", err,
			"stdout", stdout.String(),
			"stderr", stderr.String(),
		)
		return fmt.Errorf("%w: %w", ErrProvisionInstance, err)
	} else {
		log.Info(
			"initialization SSH command execution is successful",
			"error", err,
			"stdout", stdout.String(),
			"stderr", stderr.String(),
		)
	}

	return nil
}
