package ec2

import (
	"context"
	"fmt"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

var ErrProvisionInstance = fmt.Errorf("failed to perform standard software setup steps on provisioned EC2 instance")

func (d *Driver) prepareInstance(ctx context.Context, inst InstanceDeployment, net NetworkDeployment) error {
	log := clog.FromContext(ctx)
	// Marshal the private key to the OpenSSH format.
	privKey, err := inst.Keys.Private.ToSSH()
	if err != nil {
		return err // No wrapping required.
	}
	// Establish an SSH connection to the instance.
	conn, err := ssh.Connect(net.ElasticIP, portSSH, "ubuntu", privKey)
	if err != nil {
		return err // No wrapping required.
	}
	defer conn.Close()
	log.Debug("instance SSH connection is successful")
	// TODO: Obviously don't use 'os.Stdout' and 'os.Stderr' for prod lol.
	fmt.Println("BEGIN SSH <:---------------------------------------------------")
	err = ssh.ExecIn(
		conn,
		ssh.ShellBash,
		os.Stdout,
		os.Stderr,
		cmdSetInstallDockerUbuntu,
	)
	fmt.Println("  END SSH ---------------------------------------------------:>")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProvisionInstance, err)
	}
	return nil
}
