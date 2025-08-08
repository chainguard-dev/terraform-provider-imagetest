package ec2

import (
	"context"
	"fmt"
	"os"

	"github.com/chainguard-dev/clog"
)

var ErrInWait = fmt.Errorf("deadlined while waiting for EC2 instance to become reachable via SSH")

func (d *Driver) Setup(ctx context.Context) error {
	ctx = clog.WithLogger(ctx, clog.DefaultLogger())
	clog.FromContext(ctx).Info(
		"beginning driver setup",
		"driver", "ec2",
		"run_id", d.runID,
	)

	// Check if we've been told to skip resource creation.
	//
	// Scenarios like a user providing the 'instance_ip' input will set this.
	if d.SkipCreate {
		return nil
	}
	os.Exit(1)

	if err := d.init(ctx); err != nil {
		return err
	} else if err := d.postInit(ctx); err != nil {
		return err
	} else {
		return nil
	}
}
