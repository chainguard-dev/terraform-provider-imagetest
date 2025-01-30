package drivers

import (
	"context"

	"github.com/google/go-containerregistry/pkg/name"
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
