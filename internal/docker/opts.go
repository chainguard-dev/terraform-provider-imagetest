package docker

import (
	"github.com/docker/docker/client"
)

type Option func(*docker) error

func WithClient(cli *client.Client) Option {
	return func(d *docker) error {
		d.cli = cli
		return nil
	}
}
