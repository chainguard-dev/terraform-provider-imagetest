package k8s

import (
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
)

type Option func(*k8s) error

func WithRequest(request *Request) Option {
	return func(k *k8s) error {
		k.request = request
		return nil
	}
}

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

func WithNamespace(namespace string) Option {
	return func(k *k8s) error {
		k.request.Namespace = namespace
		return nil
	}
}

func WithLabels(labels map[string]string) Option {
	return func(k *k8s) error {
		if labels == nil {
			labels = make(map[string]string)
		}
		k.request.Labels = labels
		return nil
	}
}

func WithEnvs(envs map[string]string) Option {
	return func(k *k8s) error {
		if envs == nil {
			envs = make(map[string]string)
		}
		k.request.Env = envs
		return nil
	}
}

func WithUser(user int64) Option {
	return func(k *k8s) error {
		k.request.User = user
		return nil
	}
}

func WithGroup(group int64) Option {
	return func(k *k8s) error {
		k.request.Group = group
		return nil
	}
}

func WithEntrypoint(entrypoint []string) Option {
	return func(k *k8s) error {
		k.request.Entrypoint = entrypoint
		return nil
	}
}

func WithCmd(cmd []string) Option {
	return func(k *k8s) error {
		k.request.Cmd = cmd
		return nil
	}
}

func WithHostNetwork(hostNetwork bool) Option {
	return func(k *k8s) error {
		k.request.HostNetwork = hostNetwork
		return nil
	}
}

func WithDnsPolicy(dnsPolicy corev1.DNSPolicy) Option {
	return func(k *k8s) error {
		k.request.DnsPolicy = dnsPolicy
		return nil
	}
}
