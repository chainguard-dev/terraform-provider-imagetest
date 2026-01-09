package pterraform

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sandbox"
	"github.com/docker/cli/cli/connhelper"
	"github.com/docker/docker/client"
)

var _ sandbox.Runner = &dockerConnector{}

type DockerConnection struct {
	Cid            string `json:"cid"`
	Host           string `json:"host"`
	PrivateKeyPath string `json:"private_key_path"`
}

// dockerConnector is a connector that runs within a dockerConnector container.
type dockerConnector struct {
	cid  string
	resp *docker.Response
}

func (c DockerConnection) client() ([]client.Opt, error) {
	opts := []client.Opt{}

	if c.Host != "" {
		// check if we have a remote docker host
		u, err := url.Parse(c.Host)
		if err != nil {
			return nil, fmt.Errorf("invalid docker uri: %w", err)
		}

		switch u.Scheme {
		case "ssh":
			hopts := []string{
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "ServerAliveInterval=30",
				"-o", "ServerAliveCountMax=10",
			}

			if c.PrivateKeyPath != "" {
				hopts = append(hopts, "-i", c.PrivateKeyPath)
			}

			helper, err := connhelper.GetConnectionHelperWithSSHOpts(c.Host, hopts)
			if err != nil {
				return nil, err
			}

			hclient := &http.Client{
				Transport: &http.Transport{
					DialContext: helper.Dialer,
				},
			}

			opts = append(opts, client.WithHTTPClient(hclient))
			opts = append(opts, client.WithHost(helper.Host))
			opts = append(opts, client.WithDialContext(helper.Dialer))

		case "tcp":
			// TODO: No idea if this is correct
			opts = append(opts, client.WithHost(c.Host))

		default:
			return nil, fmt.Errorf("unsupported docker uri scheme: %s", u.Scheme)
		}
	}

	return opts, nil
}

func newDockerRunner(ctx context.Context, cfg *DockerConnection) (sandbox.Runner, error) {
	copts, err := cfg.client()
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	d, err := docker.New(docker.WithClientOpts(copts...))
	if err != nil {
		return nil, err
	}

	resp, err := d.Connect(ctx, cfg.Cid)
	if err != nil {
		return nil, err
	}

	return &dockerConnector{
		cid:  cfg.Cid,
		resp: resp,
	}, nil
}

// Run implements Connector.
func (d *dockerConnector) Run(ctx context.Context, cmd harness.Command) error {
	return d.resp.Run(ctx, cmd)
}
