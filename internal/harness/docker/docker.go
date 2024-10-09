package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	client "github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ harness.Harness = &dind{}

const DefaultDockerSocketPath = "/var/run/docker.sock"

type dind struct {
	Name       string
	ImageRef   name.Reference
	Networks   []client.NetworkAttachment
	Mounts     []mount.Mount
	Resources  client.ResourcesRequest
	Envs       []string
	Registries map[string]*RegistryConfig
	Volumes    []VolumeConfig

	stack  *harness.Stack
	runner func(context.Context, harness.Command) error
}

func New(opts ...Option) (harness.Harness, error) {
	h := &dind{
		ImageRef: name.MustParseReference("docker:dind"), // NOTE: This will basically always be overridden by the bundled image
		Resources: client.ResourcesRequest{
			MemoryRequest: resource.MustParse("1Gi"),
			MemoryLimit:   resource.MustParse("2Gi"),
		},
		Envs: []string{
			"IMAGETEST=true",
		},
		stack: harness.NewStack(),
	}

	for _, opt := range opts {
		if err := opt(h); err != nil {
			return nil, err
		}
	}

	return h, nil
}

// Create implements harness.Harness.
func (h *dind) Create(ctx context.Context) error {
	cli, err := client.New()
	if err != nil {
		return err
	}

	nw, err := cli.CreateNetwork(ctx, &client.NetworkRequest{
		Name: h.Name,
	})
	if err != nil {
		return fmt.Errorf("creating network: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.RemoveNetwork(ctx, nw)
	}); err != nil {
		return fmt.Errorf("adding network teardown to stack: %w", err)
	}

	dockerconfigjson, err := createDockerConfigJSON(h.Registries)
	if err != nil {
		return fmt.Errorf("creating docker config json: %w", err)
	}

	if len(h.Volumes) > 0 {
		for _, vol := range h.Volumes {
			h.Mounts = append(h.Mounts, mount.Mount{
				Type:   mount.TypeVolume,
				Source: vol.Name, // mount.Mount refers to "Source" as the name for a named volume
				Target: vol.Target,
			})
		}
	}

	resp, err := cli.Start(ctx, &client.Request{
		Name:       h.Name,
		Ref:        h.ImageRef,
		Entrypoint: []string{"/usr/bin/dockerd-entrypoint.sh"},
		Privileged: true,
		Cmd:        []string{},
		Networks:   h.Networks,
		Resources:  h.Resources,
		User:       "0:0",
		Mounts:     h.Mounts,
		Env:        h.Envs,
		Contents: []*client.Content{
			client.NewContentFromString(string(dockerconfigjson), "/root/.docker/config.json"),
		},
		ExtraHosts: []string{
			"host.docker.internal:host-gateway",
		},
		HealthCheck: &v1.HealthcheckConfig{
			Test:     []string{"CMD", "/bin/sh", "-c", "docker info"},
			Interval: 1 * time.Second,
			Retries:  30,
			Timeout:  1 * time.Minute,
		},
		Volumes: map[string]struct{}{
			"/var/lib/docker": {},
		},
	})
	if err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.Remove(ctx, resp)
	}); err != nil {
		return fmt.Errorf("adding container teardown to stack: %w", err)
	}

	h.runner = func(ctx context.Context, cmd harness.Command) error {
		return resp.Run(ctx, cmd)
	}

	return nil
}

// Run implements harness.Harness.
func (h *dind) Run(ctx context.Context, cmd harness.Command) error {
	return h.runner(ctx, cmd)
}

func (h *dind) Destroy(ctx context.Context) error {
	return h.stack.Teardown(ctx)
}

type dockerAuthEntry struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

type dockerConfig struct {
	Auths map[string]dockerAuthEntry `json:"auths,omitempty"`
}

// createDockerConfigJSON creates a Docker config.json file used by the harness for auth.
func createDockerConfigJSON(registryAuths map[string]*RegistryConfig) ([]byte, error) {
	authConfig := dockerConfig{}
	authConfig.Auths = make(map[string]dockerAuthEntry)

	for k, v := range registryAuths {
		base64Auth := v.Auth.Auth
		if base64Auth == "" {
			auth := fmt.Sprintf("%s:%s", v.Auth.Username, v.Auth.Password)
			base64Auth = base64.StdEncoding.EncodeToString([]byte(auth))
		}

		authConfig.Auths[k] = dockerAuthEntry{
			Username: v.Auth.Username,
			Password: v.Auth.Password,
			Auth:     base64Auth,
		}
	}

	dockerConfigJSON, err := json.Marshal(authConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize Docker config.dockerConfigJSON contents: %w", err)
	}

	return dockerConfigJSON, nil
}
