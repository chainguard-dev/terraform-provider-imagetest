package pod

import (
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
)

func WithImageRef(ref name.Reference) RunOpts {
	return func(o *opts) error {
		o.ImageRef = ref
		return nil
	}
}

func WithExtraEnvs(envs map[string]string) RunOpts {
	return func(o *opts) error {
		if envs == nil {
			envs = make(map[string]string)
		}
		o.ExtraEnvs = envs
		return nil
	}
}

func WithStack(stack *harness.Stack) RunOpts {
	return func(o *opts) error {
		o.stack = stack
		return nil
	}
}
