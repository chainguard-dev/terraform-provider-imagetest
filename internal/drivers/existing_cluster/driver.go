package existingcluster

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/pod"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type driver struct {
	stack *harness.Stack
	kcli  *kubernetes.Clientset
}

func NewDriver(n string, opts ...DriverOpts) (drivers.Tester, error) {
	k := &driver{
		stack: harness.NewStack(),
	}

	for _, opt := range opts {
		if err := opt(k); err != nil {
			return nil, err
		}
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		overrides := &clientcmd.ConfigOverrides{}

		kcfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

		config, err = kcfg.ClientConfig()
		if err != nil {
			return nil, err
		}
	}

	kcli, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	k.kcli = kcli

	return k, nil
}

func (k *driver) Setup(ctx context.Context) error {
	req := k.kcli.RESTClient().Get().AbsPath("/healthz").Do(ctx)

	code := 0
	req.StatusCode(&code)

	if code != 200 {
		return fmt.Errorf("kubernetes cluster is not healthy")
	}

	return nil
}

func (k *driver) Teardown(ctx context.Context) error {
	return k.stack.Teardown(ctx)
}

func (k *driver) Run(ctx context.Context, ref name.Reference) error {
	return pod.Run(ctx, k.kcli,
		pod.WithImageRef(ref),
		pod.WithExtraEnvs(map[string]string{
			"IMAGETEST_DRIVER": "existing_cluster",
		}),
		// Use our own stack since we have to try to clean up k8s resources after
		// tests are run with this driver, instead of just destroying the cluster
		// itself like we normally do
		pod.WithStack(k.stack),
	)
}
