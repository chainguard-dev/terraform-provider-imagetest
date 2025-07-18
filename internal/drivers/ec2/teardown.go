package ec2

import "context"

func (self Driver) Teardown(ctx context.Context) error {
	// Destroy instance
	return nil
}
