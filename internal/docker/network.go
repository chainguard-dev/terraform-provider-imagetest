package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/google/uuid"
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

	resp, err := d.cli.NetworkCreate(ctx, req.Name, network.CreateOptions{
		Driver:     "bridge",
		Labels:     d.withDefaultLabels(req.Labels),
		IPAM:       req.IPAM,
		EnableIPv6: &req.EnableIPv6,
	})
	if err != nil {
		return nil, err
	}

	if resp.ID == "" {
		return nil, fmt.Errorf("failed o create network: network ID is empty")
	}

	return &NetworkAttachment{
		Name: req.Name,
		ID:   resp.ID,
	}, nil
}

func (d *docker) RemoveNetwork(ctx context.Context, nw *NetworkAttachment) error {
	return d.cli.NetworkRemove(ctx, nw.ID)
}
