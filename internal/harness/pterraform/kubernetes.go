package pterraform

import (
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sandbox"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sandbox/k8s"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KubernetesConnection struct {
	Kubeconfig     string `json:"kubeconfig"`
	KubeconfigPath string `json:"kubeconfig_path"`
	SandboxImage   string `json:"sandbox_image"`
}

func (k *KubernetesConnection) runner() (sandbox.Sandbox, error) {
	cfg, err := k.parse()
	if err != nil {
		return nil, err
	}
	return k8s.NewFromConfig(cfg,
		k8s.WithRawImageRef(k.SandboxImage),
	)
}

func (k *KubernetesConnection) parse() (*rest.Config, error) {
	if k.KubeconfigPath != "" && k.Kubeconfig != "" {
		return nil, fmt.Errorf("only one of kubeconfig or kubeconfig_path can be set")
	}

	if k.KubeconfigPath != "" {
		config, err := clientcmd.BuildConfigFromFlags("", k.KubeconfigPath)
		if err != nil {
			return nil, err
		}
		return config, nil
	}

	if k.Kubeconfig != "" {
		config, err := clientcmd.RESTConfigFromKubeConfig([]byte(k.Kubeconfig))
		if err != nil {
			return nil, err
		}
		return config, nil
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
}
