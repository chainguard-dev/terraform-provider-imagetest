package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/docker/docker/api/types/mount"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/base"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
)

var _ types.Harness = &docker{}

const DefaultDockerSocketPath = "/var/run/docker.sock"

type docker struct {
	*base.Base
	id string

	container provider.Provider
}

type dockerAuthEntry struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

type dockerConfig struct {
	Auths map[string]dockerAuthEntry `json:"auths,omitempty"`
}

func New(id string, cli *provider.DockerClient, opts ...Option) (types.Harness, error) {
	options := &HarnessDockerOptions{}
	for _, opt := range opts {
		if err := opt(options); err != nil {
			return nil, err
		}
	}

	dockerSocketPath := DefaultDockerSocketPath
	if options.HostSocketPath != "" {
		dockerSocketPath = options.HostSocketPath
	}

	var managedVolumes []mount.Mount
	for _, vol := range options.ManagedVolumes {
		managedVolumes = append(managedVolumes, mount.Mount{
			Type:   mount.TypeVolume,
			Source: vol.Source,
			Target: vol.Destination,
		})
	}

	managedVolumes = append(managedVolumes, mount.Mount{
		Type:   mount.TypeVolume,
		Target: "/root/.docker",
		Source: options.ConfigVolumeName,
	})

	var mounts []mount.Mount
	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: dockerSocketPath,
		Target: DefaultDockerSocketPath,
	})

	for _, mt := range options.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: mt.Source,
			Target: mt.Destination,
		})
	}

	dockerConfigJson, err := createDockerConfigJSON(options.Registries)
	if err != nil {
		return nil, err
	}

	resources := provider.ContainerResourcesRequest{
		MemoryRequest: resource.MustParse("1Gi"),
		MemoryLimit:   resource.MustParse("2Gi"),
	}
	if options.ContainerResources != nil {
		resources = *options.ContainerResources
	}

	container := provider.NewDocker(id, cli, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Ref:        options.ImageRef,
			Entrypoint: base.DefaultEntrypoint(),
			Cmd:        base.DefaultCmd(),
			Networks:   options.Networks,
			Env:        options.Envs,
			User:       "0:0", // required to be able to change path permissions, access the socket, other tasks
			Files: []provider.File{
				{
					Contents: bytes.NewBuffer(dockerConfigJson),
					Target:   "/root/.docker/config.json",
					Mode:     0644,
				},
			},
			Resources: resources,
		},
		Mounts:         mounts,
		ManagedVolumes: managedVolumes,
	})

	return &docker{
		Base:      base.New(),
		id:        id,
		container: container,
	}, nil
}

func (h *docker) Setup() types.StepFn {
	return h.WithCreate(func(ctx context.Context) (context.Context, error) {
		if err := h.container.Start(ctx); err != nil {
			return ctx, fmt.Errorf("failed starting docker service: %w", err)
		}

		return ctx, nil
	})
}

func (h *docker) Destroy(ctx context.Context) error {
	if err := h.container.Teardown(ctx); err != nil {
		return fmt.Errorf("tearing down sandbox: %w", err)
	}

	return nil
}

func (h *docker) StepFn(config types.StepConfig) types.StepFn {
	return func(ctx context.Context) (context.Context, error) {
		log.Info(ctx, "stepping in docker container", "command", config.Command)
		r, err := h.container.Exec(ctx, provider.ExecConfig{
			Command:    config.Command,
			WorkingDir: config.WorkingDir,
		})
		if err != nil {
			return ctx, err
		}

		out, err := io.ReadAll(r)
		if err != nil {
			return ctx, err
		}

		log.Info(ctx, "finished stepping in docker container", "command", config.Command, "out", string(out))

		return ctx, nil
	}
}

// createDockerConfigJSON creates a Docker config.json file used by the harness for auth.
func createDockerConfigJSON(registryAuths map[string]*RegistryOpt) ([]byte, error) {
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
