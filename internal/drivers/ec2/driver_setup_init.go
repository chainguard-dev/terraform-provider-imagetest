package ec2

import (
	"context"

	"github.com/chainguard-dev/clog"
)

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
	log.Info("bootstrapping virtual network")
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
