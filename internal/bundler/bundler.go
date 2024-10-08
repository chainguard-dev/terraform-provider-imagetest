package bundler

import (
	"context"

	"github.com/google/go-containerregistry/pkg/name"
)

type Bundler interface {
	Bundle(ctx context.Context, repo name.Repository, layers ...Layerer) (name.Reference, error)
}
