package remote

import (
	"bytes"
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	ImagetestNamespaceName  = "imagetest"
	EntrypointContainerName = "wolfi-base"
)

// Ensure type k8s conforms to types.Harness.
var _ types.Harness = &k8s{}

type k8s struct {
	// Provide basic functions needed to make the harness operate.
	*base.Base

	// ID to identify resources created by this harness.
	Id string

	// RESTClient config.
	Config *rest.Config

	// Kubernetes Client instance.
	Client kubernetes.Interface

	// The name for the sandbox Pod.
	SandboxPodName string
}

func New(id string, kubeconfig *string) (types.Harness, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.DefaultClientConfig = &clientcmd.DefaultClientConfig

	if kubeconfig != nil {
		rules.ExplicitPath = *kubeconfig
	}

	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return &k8s{
		Base:           base.New(),
		Id:             id,
		Config:         config,
		Client:         clientset,
		SandboxPodName: fmt.Sprintf("%s-sandbox", id),
	}, nil
}

func (h *k8s) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		commonAnnotations := map[string]string{
			"dev.chainguard.imagetest":                    "true",
			"dev.chainguard.imagetest/parent-harness-ref": h.Id,
		}

		namespace, err := h.Client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:        ImagetestNamespaceName,
				Annotations: commonAnnotations,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return ctx, fmt.Errorf("failed to create %q namespace in cluster: %w", ImagetestNamespaceName, err)
		}

		_, err = h.Client.CoreV1().Pods(namespace.Name).Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        h.SandboxPodName,
				Namespace:   ImagetestNamespaceName,
				Annotations: commonAnnotations,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    EntrypointContainerName,
						Image:   "cgr.dev/chainguard/wolfi-base:latest",
						Command: []string{"sh", "-c"},
						Args:    []string{"tail", "-f", "/dev/null"},
					},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return ctx, fmt.Errorf("failed to create Pod %q in namespace %q in cluster: %w", h.SandboxPodName, ImagetestNamespaceName, err)
		}

		return ctx, nil
	})
}

func (h *k8s) Destroy(ctx context.Context) error {
	sandboxPodName := fmt.Sprintf("%s-sandbox", h.Id)
	var gracePeriodSeconds int64 = 0
	err := h.Client.CoreV1().Pods(ImagetestNamespaceName).Delete(ctx, sandboxPodName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to delete Pod '%q': %w", sandboxPodName, err)
	}

	gracePeriodSeconds = 0
	err = h.Client.CoreV1().Namespaces().Delete(ctx, ImagetestNamespaceName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to delete namespace '%q': %w", ImagetestNamespaceName, err)
	}

	// no-op
	return nil
}

func (h *k8s) StepFn(config types.StepConfig) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		restClient, err := rest.RESTClientFor(h.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to create REST client: %w", err)
		}

		execRequest := restClient.Post().
			Resource("pods").
			Name(h.SandboxPodName).
			Namespace(ImagetestNamespaceName).
			SubResource("exec")
		execRequest.VersionedParams(&corev1.PodExecOptions{
			Container: EntrypointContainerName,
			Command:   []string{config.Command},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

		executor, err := remotecommand.NewSPDYExecutor(h.Config, "POST", execRequest.URL())
		if err != nil {
			return nil, fmt.Errorf("failed to create remote executor: %w", err)
		}

		out := &bytes.Buffer{}

		err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: out,
			Stderr: out,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to execute command: %w", err)
		}

		return ctx, nil
	}
}

func (h *k8s) DebugLogCommand() string {
	return `PODLIST=$(kubectl get pods --all-namespaces --output=go-template='{{ range $pod := .items }}{{ range $status := .status.containerStatuses }}{{ if eq $status.state.waiting.reason "CrashLoopBackOff" }}{{ $pod.metadata.name }} {{ $pod.metadata.namespace }}{{ "\n" }}{{ end }}{{ end }}{{ end }}')

if [ -z "$PODLIST" ]; then
  exit 0
fi

IFS=
for POD in ${PODLIST}; do
  echo $POD | awk '{print "kubectl logs " $1 " --namespace " $2}' | xargs -I{} -t sh -c {}
done

exit 1
`
}
