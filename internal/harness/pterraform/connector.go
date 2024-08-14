package pterraform

import (
	"k8s.io/apimachinery/pkg/util/wait"
)

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
