package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
)

type NetworkRequest struct {
	// Name is the name of the network to create. If empty, a random name will be
	// generated.
	Name string
	// IPAM is the IP Address Management configuration for the network. Most of
	// the time this can be left empty and set by the daemon.
	IPAM       *network.IPAM
	Labels     map[string]string
	EnableIPv6 bool
}

type NetworkAttachment struct {
	Name string
	ID   string
}

func (d *docker) CreateNetwork(ctx context.Context, req *NetworkRequest) (*NetworkAttachment, error) {
	if req.Name == "" {
		req.Name = uuid.New().String()
	}

	if req.Labels == nil {
		req.Labels = make(map[string]string)
	}

	var (
		id      string
		lastErr error
	)
	if err := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    5,
		Cap:      1 * time.Minute,
	}, func(ctx context.Context) (bool, error) {
		resp, err := d.cli.NetworkCreate(ctx, req.Name, network.CreateOptions{
			Driver:     "bridge",
			Labels:     d.withDefaultLabels(req.Labels),
			IPAM:       req.IPAM,
			EnableIPv6: &req.EnableIPv6,
		})
		if err != nil {
			if isRetryableNetworkCreateError(err) {
				lastErr = err
				return false, nil
			}
			return false, err
		}

		if resp.ID == "" {
			return false, fmt.Errorf("failed o create network: network ID is empty")
		}

		id = resp.ID
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("creating network: %w: last error: %w", err, lastErr)
	}

	return &NetworkAttachment{
		Name: req.Name,
		ID:   id,
	}, nil
}

func (d *docker) RemoveNetwork(ctx context.Context, nw *NetworkAttachment) error {
	return d.cli.NetworkRemove(ctx, nw.ID)
}

func isRetryableNetworkCreateError(err error) bool {
	errors := []string{
		"Error response from daemon: could not find an available, non-overlapping IPv4 address pool among the defaults to assign to the network",
	}
	for _, e := range errors {
		if err != nil && strings.Contains(err.Error(), e) {
			return true
		}
	}
	// If we get here, the error was not retryable
	return false
}
