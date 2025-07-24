package ec2

import (
	"context"

	"github.com/chainguard-dev/clog"
)

func (d Driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)
	log.Info("beginning resource teardown")
	if err := d.stack.Destroy(ctx); err != nil {
		log.Error("encountered error(s) in stack teardown")
		return err
	} else {
		log.Info("stack teardown complete")
		return nil
	}
}
