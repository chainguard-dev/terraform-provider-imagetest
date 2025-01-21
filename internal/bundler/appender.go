package bundler

import (
	"context"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// appender is a bundler that appends layers to existing images,
// copying the base to the target repo.
type appender struct {
	base  name.Reference
	ropts []remote.Option
}

type AppenderOpt func(*appender) error

func NewAppender(base name.Reference, opts ...AppenderOpt) (Bundler, error) {
	a := &appender{
		base: base,
	}

	for _, opt := range opts {
		if err := opt(a); err != nil {
			return nil, err
		}
	}

	return a, nil
}

func (a *appender) Bundle(ctx context.Context, repo name.Repository, layers ...Layerer) (name.Reference, error) {
	opts := AppendOpts{
		RemoteOptions: a.ropts,
		Layers:        layers,
	}

	return Append(ctx, a.base, repo, opts)
}

func AppenderWithRemoteOptions(opts ...remote.Option) AppenderOpt {
	return func(a *appender) error {
		a.ropts = append(a.ropts, opts...)
		return nil
	}
}
