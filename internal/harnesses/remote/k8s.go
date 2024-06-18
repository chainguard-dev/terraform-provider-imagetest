package remote

import (
	"bytes"
	"context"
	"fmt"
	"net/url"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	ImagetestNamespaceNameFormat  = "imagetest-%s"
	ImagetestSandboxPodNameFormat = "%s-sandbox"
	EntrypointContainerName       = "sandbox"
	RbacResourcesNameFormat       = "rbac-%s"
	ParentHarnessRefLabel         = "dev.chainguard.imagetest/parent-harness-ref"
	ImagetestLabel                = "dev.chainguard.imagetest"
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
		Base:   base.New(),
		Id:     id,
		Config: config,
		Client: clientset,
	}, nil
}

func (h *k8s) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		var rootUserID int64 = 0
		commonLabels := map[string]string{
			ImagetestLabel:        "true",
			ParentHarnessRefLabel: h.Id,
		}

		namespaceName := fmt.Sprintf(ImagetestNamespaceNameFormat, h.Id)
		sandboxPodName := fmt.Sprintf(ImagetestSandboxPodNameFormat, h.Id)
		rbacResourcesName := fmt.Sprintf(RbacResourcesNameFormat, h.Id)

		_, err := h.Client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespaceName,
				Labels: commonLabels,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return ctx, fmt.Errorf("failed to create %q Namespace in cluster: %w", namespaceName, err)
		}

		err = h.createRbacResources(ctx, namespaceName, commonLabels)
		if err != nil {
			return ctx, fmt.Errorf("failed to create RBAC resources for cluster: %w", err)
		}

		podClient := h.Client.CoreV1().Pods(namespaceName)

		_, err = podClient.Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sandboxPodName,
				Namespace: namespaceName,
				Labels:    commonLabels,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    EntrypointContainerName,
						Image:   "cgr.dev/chainguard/kubectl:latest-dev",
						Command: []string{"tail", "-f", "/dev/null"},
					},
				},
				ServiceAccountName: rbacResourcesName,
				SecurityContext: &corev1.PodSecurityContext{
					RunAsUser: &rootUserID,
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return ctx, fmt.Errorf("failed to create Pod %q in Namespace %q in cluster: %w", sandboxPodName, namespaceName, err)
		}

		err = h.waitForPod(ctx, podClient)
		if err != nil {
			return ctx, fmt.Errorf("failed to get Pod %q in Namespace %q in cluster: %w", sandboxPodName, namespaceName, err)
		}

		return ctx, nil
	})
}

func (h *k8s) waitForPod(ctx context.Context, podClient v1.PodInterface) error {
	watcher, err := podClient.Watch(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", ParentHarnessRefLabel, h.Id),
		Watch:         true,
	})
	if err != nil {
		return err
	}

	defer watcher.Stop()

	for {
		select {
		case event := <-watcher.ResultChan():
			watchedPod, ok := event.Object.(*corev1.Pod)
			if !ok {
				log.Info(ctx, "object was not Pod", watchedPod)
				continue
			}

			if watchedPod.Status.Phase == corev1.PodRunning {
				return nil
			}

		case <-ctx.Done():
			log.Info(ctx, "exiting because context is done")
			return nil
		}
	}
}

func (h *k8s) createRbacResources(ctx context.Context, namespaceName string, commonLabels map[string]string) error {
	rbacResourcesName := fmt.Sprintf(RbacResourcesNameFormat, h.Id)

	_, err := h.Client.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   rbacResourcesName,
			Labels: commonLabels,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get", "create", "list", "delete", "watch"},
				APIGroups: []string{""},
				Resources: []string{"pods", "configmaps", "secrets", "serviceaccounts", "services", "namespaces"},
			},
			{
				Verbs:     []string{"get", "create", "list", "delete", "watch"},
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "daemonsets", "replicasets", "statefulsets"},
			},
			{
				Verbs:     []string{"get", "create", "list", "delete", "watch"},
				APIGroups: []string{"batch"},
				Resources: []string{"jobs", "cronjobs"},
			},
			{
				Verbs:     []string{"get", "create", "list", "delete", "watch"},
				APIGroups: []string{"storage"},
				Resources: []string{"jobs", "cronjobs"},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	_, err = h.Client.CoreV1().ServiceAccounts(namespaceName).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacResourcesName,
			Namespace: namespaceName,
			Labels:    commonLabels,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	_, err = h.Client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   rbacResourcesName,
			Labels: commonLabels,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     rbacResourcesName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      rbacResourcesName,
				Namespace: namespaceName,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (h *k8s) Destroy(ctx context.Context) error {
	sandboxPodName := fmt.Sprintf(ImagetestSandboxPodNameFormat, h.Id)
	namespaceName := fmt.Sprintf(ImagetestNamespaceNameFormat, h.Id)
	rbacResourcesName := fmt.Sprintf(RbacResourcesNameFormat, h.Id)

	var gracePeriodSeconds int64 = 0

	err := h.Client.RbacV1().ClusterRoleBindings().Delete(ctx, rbacResourcesName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to delete RoleBinding %q: %w", rbacResourcesName, err)
	}

	err = h.Client.RbacV1().ClusterRoles().Delete(ctx, rbacResourcesName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to delete Role %q: %w", rbacResourcesName, err)
	}

	err = h.Client.CoreV1().ServiceAccounts(namespaceName).Delete(ctx, rbacResourcesName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to delete ServiceAccount %q: %w", rbacResourcesName, err)
	}

	err = h.Client.CoreV1().Pods(namespaceName).Delete(ctx, sandboxPodName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to delete Pod %q: %w", sandboxPodName, err)
	}

	err = h.Client.CoreV1().Namespaces().Delete(ctx, namespaceName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to delete Namespace %q: %w", namespaceName, err)
	}

	// no-op
	return nil
}

func (h *k8s) StepFn(config types.StepConfig) types.StepFn {
	// This code was heavily based on the code for kubectl exec: https://github.com/kubernetes/kubernetes/blob/a147693deb2e7f040cf367aae4a7ae5d1cb3e7aa/staging/src/k8s.io/kubectl/pkg/cmd/exec/exec.go
	return func(ctx context.Context) (context.Context, error) {
		h.Config.GroupVersion = &schema.GroupVersion{
			Group:   "",
			Version: "v1",
		}
		h.Config.APIPath = "/api"
		h.Config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

		restClient, err := rest.RESTClientFor(h.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to create REST client: %w", err)
		}

		execRequest := restClient.Post().
			Resource("pods").
			Name(fmt.Sprintf(ImagetestSandboxPodNameFormat, h.Id)).
			Namespace(fmt.Sprintf(ImagetestNamespaceNameFormat, h.Id)).
			SubResource("exec")
		execRequest.VersionedParams(&corev1.PodExecOptions{
			Container: EntrypointContainerName,
			Command:   []string{"sh", "-c", config.Command},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

		execRequestURL := execRequest.URL()
		executor, err := h.createExecutor(execRequestURL)
		if err != nil {
			return ctx, err
		}

		errBuf := &bytes.Buffer{}
		outBuf := &bytes.Buffer{}

		err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
			Tty:    false,
			Stdout: outBuf,
			Stderr: errBuf,
		})

		if err != nil {
			return nil, fmt.Errorf("failed to run command\n"+
				"stdout output: \n\t%s\n\n"+
				"stderr output: \n\t%s\n\n"+
				"error message: %w", outBuf.String(), errBuf.String(), err)
		}

		return ctx, nil
	}
}

// createExecutor creates a remote executor to run commands in the target cluster.
// This code was heavily based on the code for kubectl exec: https://github.com/kubernetes/kubernetes/blob/a147693deb2e7f040cf367aae4a7ae5d1cb3e7aa/staging/src/k8s.io/kubectl/pkg/cmd/exec/exec.go
func (h *k8s) createExecutor(execRequestURL *url.URL) (remotecommand.Executor, error) {
	executor, err := remotecommand.NewSPDYExecutor(h.Config, "POST", execRequestURL)
	if err != nil {
		return nil, err
	}

	websocketExec, err := remotecommand.NewWebSocketExecutor(h.Config, "GET", execRequestURL.String())
	if err != nil {
		return nil, err
	}
	executor, err = remotecommand.NewFallbackExecutor(websocketExec, executor, httpstream.IsUpgradeFailure)
	if err != nil {
		return nil, err
	}

	return executor, nil
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
