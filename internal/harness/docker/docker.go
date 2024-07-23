package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/docker/docker/api/types/mount"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
)

var _ harness.Harness = &docker{}

const DefaultDockerSocketPath = "/var/run/docker.sock"

type docker struct {
	id string

	container provider.Provider
}

func New(id string, cli *provider.DockerClient, opts ...Option) (harness.Harness, error) {
	options := &HarnessDockerOptions{
		ContainerResources: provider.ContainerResourcesRequest{
			MemoryRequest: resource.MustParse("1Gi"),
			MemoryLimit:   resource.MustParse("2Gi"),
		},
	}

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
			VolumeOptions: &mount.VolumeOptions{
				Labels: provider.DefaultLabels(),
			},
		})
	}

	managedVolumes = append(managedVolumes, mount.Mount{
		Type:   mount.TypeVolume,
		Target: "/root/.docker",
		Source: options.ConfigVolumeName,
		VolumeOptions: &mount.VolumeOptions{
			Labels: provider.DefaultLabels(),
		},
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

	container := provider.NewDocker(id, cli, provider.DockerRequest{
		ContainerRequest: provider.ContainerRequest{
			Ref:        options.ImageRef,
			Entrypoint: harness.DefaultEntrypoint(),
			Cmd:        harness.DefaultCmd(),
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
			Resources: options.ContainerResources,
			Labels:    provider.MainHarnessLabel(),
		},
		Mounts:         mounts,
		ManagedVolumes: managedVolumes,
	})

	return &docker{
		id:        id,
		container: container,
	}, nil
}

// Create implements harness.Harness.
func (h *docker) Create(ctx context.Context) error {
	return h.container.Start(ctx)
}

// Run implements harness.Harness.
func (h *docker) Run(ctx context.Context, cmd harness.Command) error {
	log.Info(ctx, "stepping in docker container with command", "command", cmd.Args)
	r, err := h.container.Exec(ctx, provider.ExecConfig{
		Command:    cmd.Args,
		WorkingDir: cmd.WorkingDir,
	})
	if err != nil {
		return err
	}

	out, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	log.Info(ctx, "finished stepping in docker container", "command", cmd.Args, "out", string(out))

	return nil
}

func (h *docker) DebugLogCommand() string {
	// TODO implement something here
	return ""
}

func (h *docker) Destroy(ctx context.Context) error {
	return h.container.Teardown(ctx)
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
