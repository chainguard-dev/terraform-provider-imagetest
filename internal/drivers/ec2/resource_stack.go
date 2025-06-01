package ec2

import (
	"context"
	"errors"
	"fmt"
)

type ResourceStack struct {
	Resources []Resource
}

func (self *ResourceStack) Push(resource Resource) {
	self.Resources = append(self.Resources, resource)
}

var (
	ErrResourceDestroyFailed = fmt.Errorf("failed to destroy resource")
	ErrResourceStillExists   = fmt.Errorf(
		"the resource does not reflect a " +
			"destroyed state following the destroy() call",
	)
)

func (self *ResourceStack) Destroy(ctx context.Context) error {
	var errs error

	for {
		select {
		case <-ctx.Done():
			return errors.Join(errs, context.DeadlineExceeded)
		default:
		}

		if len(self.Resources) == 0 {
			return errs
		}

		// Slice off the next resource
		next := self.Resources[len(self.Resources)-1]
		self.Resources = self.Resources[:len(self.Resources)-1]

		// Attempt to destroy it
		err := next.Destroy(ctx)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("%w: %w", ErrResourceDestroyFailed, err))
		} else if next.Status() != StatusDestroyed {
			errs = errors.Join(errs, fmt.Errorf("%w (%s)", ErrResourceStillExists, next.ID()))
		}
	}
}
