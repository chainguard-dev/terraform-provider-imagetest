package ec2

import (
	"context"
	"errors"
	"slices"
)

type (
	stack struct {
		Destructors []destructor
	}
	destructor func(ctx context.Context) error
)

// Push adds a destructor to the 'Destructors' slice, to be destroyed in the
// reverse order they were added.
func (s *stack) Push(d destructor) {
	s.Destructors = append(s.Destructors, d)
}

// Destroy calls all accumulated destructors in the reverse order they were
// added, returning all encountered errors joined.
func (s *stack) Destroy(ctx context.Context) error {
	var errs error
	for _, destructor := range slices.Backward(s.Destructors) {
		errs = errors.Join(errs, destructor(ctx))
	}
	return errs
}
