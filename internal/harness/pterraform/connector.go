package pterraform

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"k8s.io/apimachinery/pkg/util/wait"
)

type Runner interface {
	Run(ctx context.Context, cmd harness.Command) error
}

type Connection struct {
	Kubernetes *KubernetesConnection `json:"kubernetes"`
	Docker     *DockerConnection     `json:"docker"`
	// Retry is the retry configuration for the connection
	Retry *ConnectionRetry `json:"retry"`

	backoff wait.Backoff
}

type ConnectionRetry struct {
	Attempts int     `json:"attempts"`
	Delay    string  `json:"delay"`
	Factor   float64 `json:"factor"`
}
