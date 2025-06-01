package ec2

import "context"

type Resource interface {
	ID() string
	Status() ResourceStatus
	Init(ctx context.Context) error
	Destroy(ctx context.Context) error
}

type ResourceStatus uint8

const (
	StatusNotInitialized ResourceStatus = 1 + iota
	StatusInitializing
	StatusRunning
	StatusDestroying
	StatusDestroyed
)
