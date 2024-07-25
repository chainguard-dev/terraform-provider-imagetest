package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	client "github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ harness.Harness = &docker{}

const DefaultDockerSocketPath = "/var/run/docker.sock"

type docker struct {
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
	h := &docker{
		ImageRef: name.MustParseReference("cgr.dev/chainguard/docker-cli:latest-dev"),
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
func (h *docker) Create(ctx context.Context) error {
	cli, err := client.New()
	if err != nil {
		return err
	}

	nw, err := cli.CreateNetwork(ctx, &client.NetworkRequest{})
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

	mounts := append(h.Mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: "/var/run/docker.sock",
		Target: "/var/run/docker.sock",
	})

	if len(h.Volumes) > 0 {
		for _, vol := range h.Volumes {
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeVolume,
				Source: vol.Name, // mount.Mount refers to "Source" as the name for a named volume
				Target: vol.Target,
			})
		}
	}

	resp, err := cli.Start(ctx, &client.Request{
		Name:       h.Name,
		Ref:        h.ImageRef,
		Entrypoint: harness.DefaultEntrypoint(),
		Cmd:        harness.DefaultCmd(),
		Networks:   h.Networks,
		Resources:  h.Resources,
		User:       "0:0",
		Mounts:     mounts,
		Env:        h.Envs,
		Contents: []*client.Content{
			client.NewContentFromString(string(dockerconfigjson), "/root/.docker/config.json"),
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
func (h *docker) Run(ctx context.Context, cmd harness.Command) error {
	return h.runner(ctx, cmd)
}

func (h *docker) DebugLogCommand() string {
	// TODO implement something here
	return ""
}

func (h *docker) Destroy(ctx context.Context) error {
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
