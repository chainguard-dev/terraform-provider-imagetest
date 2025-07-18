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

func (self *Stack) Push(d Destructor) {
	self.Destructors = append(self.Destructors, d)
}

func (self *Stack) Destroy(ctx context.Context) error {
	var errs error
	for i := len(self.Destructors) - 1; i >= 0; i -= 1 {
		errs = errors.Join(errs, self.Destructors[i](ctx))
	}
	return errs
}
