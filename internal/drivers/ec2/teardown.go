package ec2

import "context"

func (self Driver) Teardown(ctx context.Context) error {
	return self.stack.Destroy(ctx)
}
