package ec2

import "context"

type Resource interface {
	ID() string
	Status() ResourceStatus
	Destroy(ctx context.Context) error
}

type ResourceStatus uint8

const (
	StatusLive ResourceStatus = 1 + iota
	StatusDestroying
	StatusDestroyed
)

type (
	Initializer func(ctx context.Context) error
	Destroyer   func(ctx context.Context) error
)

func NewGenericResource(id string, destroy Destroyer) Resource {
	return &genericResource{
		id:        id,
		destroyer: destroy,
		status:    StatusLive,
	}
}

var _ Resource = (*genericResource)(nil)

type genericResource struct {
	id          string
	destroyer   Destroyer
	initializer Initializer
	status      ResourceStatus
}

func (self *genericResource) ID() string {
	return self.id
}

func (self *genericResource) Status() ResourceStatus {
	return self.status
}

func (self *genericResource) Destroy(ctx context.Context) error {
	// Indicate destruction starting
	self.status = StatusDestroying
	if err := self.destroyer(ctx); err != nil {
		// Leave the status in `Destroying` and return the error
		return err
	}
	// Indicate destruction complete without error
	self.status = StatusDestroyed
	return nil
}
