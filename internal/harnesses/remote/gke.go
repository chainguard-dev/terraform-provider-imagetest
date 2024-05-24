package remote

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

// Ensure type gke conforms to types.Harness
var _ types.Harness = &gke{}

type gke struct {
	// Provide basic functions needed to make the harness operate.
	*base.Base
}

func (h *gke) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		// no-op for now
		return ctx, nil
	})
}

func (h *gke) Destroy(_ context.Context) error {
	// no-op for now
	return nil
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
