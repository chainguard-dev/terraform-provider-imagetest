package ec2

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

// postInit serves as the initialization counterpart to 'init'. Here is where
// all user-provided configuration occurs within the EC2 instance.
//
// See 'init' for more details.
func (d *Driver) postInit(ctx context.Context) error {
	log := clog.FromContext(ctx)

	// Skip out early if we have no user-provided commands.
	if len(d.Exec.Commands) == 0 {
		log.Info("no user-provided commands present, skipping post-init")
		return nil
	}
	log.Info(
		"found user-provided instance initialization commands",
		"count", len(d.Exec.Commands),
	)

	// Convert the ED25519 private key to an 'ssh.Signer'.
	signer, err := d.Instance.Keys.Private.ToSSH()
	if err != nil {
		return err // No annotation required.
	}
	log.Debug("ED25519 private key marshal to 'ssh.Signer' is successful")

	// Establish the SSH connection.
	log.Info(
		"connecting to EC2 instance",
		"user", d.Exec.User,
		"host", d.Network.ElasticIP,
		"port", portSSH,
	)
	client, err := ssh.Connect(d.Network.ElasticIP, portSSH, d.Exec.User, signer)
	if err != nil {
		return fmt.Errorf("failed to connect to instance via SSH: %w", err)
	}
	log.Info("SSH connection to EC2 instance is successful")

	// Convert the environment map to a series of export statements we can feed
	// right into each Bash session.
	//
	// TODO: The SSH RFC specifies an 'env' request which allows providing
	// environment variables prior to the actual process execution. It would be a
	// much cleaner implementation to do it that way!
	cmds := []string{cmdStdOpts}
	// Append the standard shell options.
	for k, v := range d.Exec.Env {
		export := fmt.Sprintf("export %s=%s", k, v)
		cmds = append(cmds, export)
		log.Debug("adding environment variable export", "name", k, "value", v)
	}

	// Append the user-provided commands to the export statements.
	cmds = append(cmds, d.Exec.Commands...)

	// Detonate all initialization commands on the remote instance.
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if !debugSet() {
		err = ssh.ExecIn(client, d.Exec.Shell, stdout, stderr, cmds...)
	} else {
		err = ssh.ExecIn(client, d.Exec.Shell, os.Stdout, os.Stderr, cmds...)
	}
	if err != nil {
		log.Error(
			"SSH command execution failed",
			"error", err,
			"stdout", stdout.String(),
			"stderr", stderr.String(),
		)
		return fmt.Errorf("encountered error in command execution: %w", err)
	} else {
		log.Info(
			"SSH command execution is successful",
			"error", err,
			"stdout", stdout.String(),
			"stderr", stderr.String(),
		)
	}

	return nil
}
