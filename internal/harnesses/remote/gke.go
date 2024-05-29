package remote

import (
	"context"
	"fmt"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"k8s.io/apimachinery/pkg/api/errors"
)

// Ensure type gke conforms to types.Harness.
var _ types.Harness = &gke{}

const clusterNameFormat = "projects/%s/locations/%s/clusters/%s"

type gke struct {
	// Provide basic functions needed to make the harness operate.
	*base.Base

	// The name for the cluster
	ClusterName string

	// The project where the cluster should be created
	ProjectName string

	// The location where the cluster should be created
	Location string
}

func (h *gke) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		client, err := createClusterManagerClient(ctx)
		if err != nil {
			return ctx, fmt.Errorf("failed to create cluster manager client: %w", err)
		}

		_, err = client.CreateCluster(ctx, &containerpb.CreateClusterRequest{
			Cluster: &containerpb.Cluster{
				Name: h.ClusterName,
				Autopilot: &containerpb.Autopilot{
					Enabled: true,
				},
			},
			Parent: fmt.Sprintf("projects/%s/locations/%s", h.ProjectName, h.Location),
		})
		if err != nil {
			return ctx, fmt.Errorf("failed to create cluster on project %s and location %s: %w", h.ProjectName, h.Location, err)
		}

		for { // wait until provisioning is concluded
			cluster, err := client.GetCluster(ctx, &containerpb.GetClusterRequest{
				Name: fmt.Sprintf(clusterNameFormat, h.ProjectName, h.Location, h.ClusterName),
			})
			if err != nil {
				return ctx, fmt.Errorf("failed to provision cluster: %w", err)
			}

			if containerpb.Cluster_RUNNING == cluster.Status {
				return ctx, nil
			}

			log.Info(ctx, "waiting for cluster to provision...")
		}
	})
}

func (h *gke) Destroy(ctx context.Context) error {
	client, err := createClusterManagerClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create cluster manager client: %w", err)
	}

	_, err = client.DeleteCluster(ctx, &containerpb.DeleteClusterRequest{
		Name: fmt.Sprintf(clusterNameFormat, h.ProjectName, h.Location, h.ClusterName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	for { // wait until deletion is concluded
		_, err := client.GetCluster(ctx, &containerpb.GetClusterRequest{
			Name: fmt.Sprintf(clusterNameFormat, h.ProjectName, h.Location, h.ClusterName),
		})
		if err != nil {
			if errors.IsNotFound(err) {
				// successfully deleted
				return nil
			}
			return fmt.Errorf("failed to delete cluster: %w", err)
		}

		log.Info(ctx, "waiting for cluster to delete...")
	}
}

func (h *gke) StepFn(config types.StepConfig) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		// no-op for now
		return ctx, nil
	}
}

func (h *gke) DebugLogCommand() string {
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

func createClusterManagerClient(ctx context.Context) (*container.ClusterManagerClient, error) {
	return container.NewClusterManagerClient(ctx)
}
