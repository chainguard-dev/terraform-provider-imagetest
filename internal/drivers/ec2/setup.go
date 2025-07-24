package ec2

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/clog"
)

var (
	ErrInstanceTypeSelection = fmt.Errorf("failed instance selection")
	ErrAMISelection          = fmt.Errorf("failed AMI selection")
	ErrInWait                = fmt.Errorf("deadlined while waiting for EC2 instance to become reachable via SSH")
)

func (d *Driver) Setup(ctx context.Context) (err error) {
	log := clog.FromContext(ctx).With("driver", "ec2", "run_id", d.runID)
	log.Info("beginning driver setup")
	// 'Setup' uses a named return on the error specifically for this function. By
	// giving it a name, we can defer a check of that variable. If it's not nil,
	// we can teardown any resources we created.
	defer func() {
		if err != nil {
			log.Error("error encountered in driver setup, beginning teardown")
			if err := d.stack.Destroy(ctx); err != nil {
				log.Error("encountered error in stack teardown", "error", err)
			} else {
				log.Info("stack teardown successful")
			}
		} else {
			log.Info("driver setup complete")
		}
	}()
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
	// Prepare up the EC2 instance.
	err = d.prepareInstance(ctx, d.instance, d.net)
	if err != nil {
		return err
	}
	log.Info("EC2 instance configuration complete")
	return nil
}
