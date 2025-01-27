package bundler

import (
	"context"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

type Bundler interface {
	Bundle(ctx context.Context, repo name.Repository, layers ...v1.Layer) (name.Reference, error)
}
