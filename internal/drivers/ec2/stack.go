package ec2

import (
	"context"
	"errors"
)

type (
	Stack struct {
		Destructors []Destructor
	}
	Destructor func(ctx context.Context) error
)

func (s *Stack) Push(d Destructor) {
	s.Destructors = append(s.Destructors, d)
}

func (s *Stack) Destroy(ctx context.Context) error {
	var errs error
	for i := len(s.Destructors) - 1; i >= 0; i -= 1 {
		errs = errors.Join(errs, s.Destructors[i](ctx))
	}
	return errs
}
