package sandbox

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Sandbox is an interface for defining the sandbox where tests are executed.
// Each instance of a sandbox is responsible for the lifecycle of only one
// sandbox environment. Configuration for the sandbox is handled at
// instantiation.
type Sandbox interface {
	Start(ctx context.Context) (Runner, error)
	Destroy(ctx context.Context) error
}

type Runner interface {
	Run(ctx context.Context, cmd harness.Command) error
}

// Request is the common configuration options for all sandbox types. This is
// essentially a wrapper around a Pod spec scoped specifically for a sandbox usage.
type Request struct {
	Ref        name.Reference
	Name       string
	Namespace  string
	WorkingDir string
	User       int64
	Group      int64
	Env        map[string]string
	Entrypoint []string
	Cmd        []string
	Resources  ResourceRequest
	Labels     map[string]string
}

// ResourceRequest is really just a wrapper around a pods resource request.
type ResourceRequest struct {
	Limits   map[string]resource.Quantity
	Requests map[string]resource.Quantity
}
