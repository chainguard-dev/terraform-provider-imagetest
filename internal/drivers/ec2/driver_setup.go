package ec2

import (
	"bytes"
	"context"
	"fmt"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

var ErrInWait = fmt.Errorf("deadlined while waiting for EC2 instance to become reachable via SSH")

func (d *Driver) Setup(ctx context.Context) error {
	ctx = clog.WithLogger(ctx, clog.DefaultLogger())
	clog.FromContext(ctx).Info(
		"beginning driver setup",
		"driver", "ec2",
		"run_id", d.runID,
	)
	if err := d.init(ctx); err != nil {
		return err
	} else if err := d.postInit(ctx); err != nil {
		return err
	} else {
		return nil
	}
}

// init serves as the driver's internal resource construction method. All VPC
// and EC2 instance resources are built here. The EC2 instance also has all
// configuration we define as "standard" (ex: install Docker) applied at this
// stage to make the separation between our standard template configuration and
// user-provided functionality very clear.
func (d *Driver) init(ctx context.Context) (err error) {
	log := clog.FromContext(ctx)

	// Bootstrap the virtual network.
	//
	// TODO: In the future it'd be great to be able to bring-your-own VPC subnet
	// ID and just attach the EC2 instance to that.
	d.net, err = d.deployNetwork(ctx)
	if err != nil {
		return err
	}
	log.Info("virtual network setup complete")

	// Deploy the EC2 instance.
	d.instance, err = d.deployInstance(ctx, d.net)
	if err != nil {
		return err
	}
	log.Info("EC2 instance deployment complete")

	// Prepare the EC2 instance.
	err = d.prepareInstance(ctx, d.instance, d.net)
	if err != nil {
		return err
	}
	log.Info("EC2 instance configuration complete")

	return nil
}

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
	signer, err := d.instance.Keys.Private.ToSSH()
	if err != nil {
		return err // No annotation required.
	}
	log.Debug("ED25519 private key marshal to 'ssh.Signer' is successful")

	// Establish the SSH connection.
	log.Info(
		"connecting to EC2 instance",
		"user", d.Exec.User,
		"host", d.net.ElasticIP,
		"port", portSSH,
	)
	client, err := ssh.Connect(d.net.ElasticIP, portSSH, d.Exec.User, signer)
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
	cmds := make([]string, 0, len(d.Exec.Env)+1)
	// Append the standard shell options.
	cmds = append(cmds, cmdStdOpts)
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
	err = ssh.ExecIn(client, d.Exec.Shell, stdout, stderr, cmds...)
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
