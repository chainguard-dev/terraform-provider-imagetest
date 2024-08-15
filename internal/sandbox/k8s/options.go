package k8s

import "github.com/google/go-containerregistry/pkg/name"

type Option func(*k8s) error

func WithRawImageRef(ref string) Option {
	return func(k *k8s) error {
		if ref == "" {
			return nil
		}
		ref, err := name.ParseReference(ref)
		if err != nil {
			return err
		}
		k.request.Ref = ref
		return nil
	}
}

func WithImageRef(ref name.Reference) Option {
	return func(k *k8s) error {
		k.request.Ref = ref
		return nil
	}
}

func WithGracePeriod(gracePeriod int64) Option {
	return func(k *k8s) error {
		k.gracePeriod = gracePeriod
		return nil
	}
}
