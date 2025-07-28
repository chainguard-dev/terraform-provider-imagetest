package ec2

import (
	"context"
	"os"

	"github.com/chainguard-dev/clog"
)

// Teardown destroys all resources in the reverse order they were created.
//
// NOTE: All destructors are queued immediately following their resources
// creation. See 'doc.go' '# Phase: Teardown' for more details.
func (d *Driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)
	// If the appropriate env var is set, leave the resources behind when we're
	// done.
	//
	// NOTE: The documentation for this environment variable's usage specifies a
	// value of 'true' is required to skip teardown. However, implementations
	// around the codebase simply look for the existence of the variable. The
	// below aligns with the existing implementations.
	if _, ok := os.LookupEnv("IMAGETEST_SKIP_TEARDOWN"); ok {
		log.Info("IMAGETEST_SKIP_TEARDOWN is set, skipping cleanup")
		return nil
	}
	log.Info("beginning resource teardown")

	// Destroy it all!
	if err := d.stack.Destroy(ctx); err != nil {
		log.Error("encountered error(s) in stack teardown", "error", err)
		return err
	} else {
		log.Info("stack teardown complete")
		return nil
	}
}
