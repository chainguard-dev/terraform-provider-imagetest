package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ harness.Harness = &dind{}

const (
	dindCertDir = "/imagetest/certs"
)

type dind struct {
	Name       string
	ImageRef   name.Reference
	Networks   []docker.NetworkAttachment
	Mounts     []mount.Mount
	Resources  docker.ResourcesRequest
	Envs       []string
	Registries map[string]*RegistryConfig
	Volumes    []VolumeConfig

	stack  *harness.Stack
	runner func(context.Context, harness.Command) error
}

func New(opts ...Option) (harness.Harness, error) {
	h := &dind{
		ImageRef: name.MustParseReference("cgr.dev/chainguard/docker-cli:latest-dev"),
		Resources: docker.ResourcesRequest{
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

	// translate volumes to mounts
	if len(h.Volumes) > 0 {
		for _, vol := range h.Volumes {
			h.Mounts = append(h.Mounts, mount.Mount{
				Type:   mount.TypeVolume,
				Source: vol.Name, // mount.Mount refers to "Source" as the name for a named volume
				Target: vol.Target,
			})
		}
	}

	return h, nil
}

// Create implements harness.Harness.
func (h *dind) Create(ctx context.Context) error {
	cli, err := docker.New()
	if err != nil {
		return err
	}

	dresp, err := h.startDaemon(ctx, cli)
	if err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	if err := h.startSandbox(ctx, cli, dresp); err != nil {
		return fmt.Errorf("creating sandbox: %w", err)
	}

	return nil
}

func (h *dind) startDaemon(ctx context.Context, cli *docker.Client) (*docker.Response, error) {
	nw, err := cli.CreateNetwork(ctx, &docker.NetworkRequest{})
	if err != nil {
		return nil, fmt.Errorf("creating network: %w", err)
	}
	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.RemoveNetwork(ctx, nw)
	}); err != nil {
		return nil, fmt.Errorf("adding network teardown to stack: %w", err)
	}

	resp, err := cli.Start(ctx, &docker.Request{
		Name:       h.Name,
		Ref:        name.MustParseReference("docker:dind"),
		Privileged: true,
		Networks: append(h.Networks, docker.NetworkAttachment{
			Name: nw.Name,
			ID:   nw.ID,
		}),
		Env: []string{
			fmt.Sprintf("DOCKER_TLS_CERTDIR=%s", dindCertDir),
		},
		HealthCheck: &container.HealthConfig{
			Test:        []string{"CMD", "/bin/sh", "-c", "docker info"},
			Interval:    1 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     5,
			StartPeriod: 1 * time.Second,
		},
		Mounts: h.Mounts,
	})
	if err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}

	if err := h.stack.Add(func(ctx context.Context) error {
		return cli.Remove(ctx, resp)
	}); err != nil {
		return nil, fmt.Errorf("adding container teardown to stack: %w", err)
	}

	return resp, nil
}

func (h *dind) startSandbox(ctx context.Context, cli *docker.Client, dresp *docker.Response) error {
	dockerconfigjson, err := createDockerConfigJSON(h.Registries)
	if err != nil {
		return fmt.Errorf("creating docker config json: %w", err)
	}

	nws := make(map[string]struct{})

	// Attach the sandbox to any networks k3s is also a part of, excluding any
	// invalid networks or networks already attached (the daemon cannot deconflict
	// these)
	for nn, nw := range dresp.NetworkSettings.Networks {
		if nn == "" {
			continue
		}
		if _, ok := nws[nn]; !ok {
			nws[nn] = struct{}{}
			h.Networks = append(h.Networks, docker.NetworkAttachment{
				Name: nn,
				ID:   nw.NetworkID,
			})
		}
	}

	certs, err := h.certContents(ctx, dresp)
	if err != nil {
		return fmt.Errorf("getting certs from dind: %w", err)
	}

	name := dresp.Name + "-sandbox"

	resp, err := cli.Start(ctx, &docker.Request{
		Name:       name,
		Ref:        h.ImageRef,
		Entrypoint: harness.DefaultEntrypoint(),
		Cmd:        harness.DefaultCmd(),
		Networks:   h.Networks,
		Resources:  h.Resources,
		User:       "0:0",
		Env: append(h.Envs,
			fmt.Sprintf("DOCKER_HOST=tcp://%s:2376", dresp.Config.Hostname),
			"DOCKER_TLS_VERIFY=1",
			fmt.Sprintf("DOCKER_CERT_PATH=%s", filepath.Join(dindCertDir, "client")),
		),
		Mounts: h.Mounts,
		Contents: append([]*docker.Content{
			docker.NewContentFromString(string(dockerconfigjson), "/root/.docker/config.json"),
		}, certs...),
		NetworkMode: fmt.Sprintf("container:%s", dresp.ID),
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

// certContents grabs the certs from the dind container and returns them as
// content. We could use a mount here, but we're trying to get away from bind
// mounts and realistically we're only inefficiently copying 3 very small files
// here.
func (h *dind) certContents(ctx context.Context, resp *docker.Response) ([]*docker.Content, error) {
	rc, err := resp.GetFromContainer(ctx, filepath.Join(dindCertDir, "client"))
	if err != nil {
		return nil, fmt.Errorf("getting certs from container: %w", err)
	}

	tr := tar.NewReader(rc)

	contents := map[string]*docker.Content{
		"ca.pem":   nil,
		"cert.pem": nil,
		"key.pem":  nil,
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading certs from container: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		name := filepath.Base(hdr.Name)

		switch name {
		case "ca.pem", "cert.pem", "key.pem":
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, tr); err != nil {
				return nil, fmt.Errorf("reading certs from container: %w", err)
			}

			contents[name] = docker.NewContentFromString(buf.String(), filepath.Join("/imagetest/certs/client", name))
		}
	}

	if err := rc.Close(); err != nil {
		return nil, fmt.Errorf("closing certs reader: %w", err)
	}

	c := []*docker.Content{}
	for k, v := range contents {
		if v == nil {
			return nil, fmt.Errorf("no %s found in dind container", k)
		}
		c = append(c, v)
	}

	return c, nil
}
