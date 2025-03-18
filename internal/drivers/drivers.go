package drivers

import (
	"context"

	"github.com/google/go-containerregistry/pkg/name"
)

const (
	// LogAttributeKey is the key where log lines from drivers will be surfaced.
	LogAttributeKey = "driver_log"
)

type Tester interface {
	// Setup creates the driver's resources, it must be run before Run() is
	// available
	Setup(context.Context) error
	// Teardown destroys the driver's resources
	Teardown(context.Context) error
	// Run takes a container and runs it
	Run(context.Context, name.Reference) error
}
