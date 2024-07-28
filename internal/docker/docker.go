// docker holds useful things for interacting with docker
package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
)

type docker struct {
	cli   *client.Client
	copts []client.Opt
}

type Request struct {
	Ref          name.Reference
	Name         string
	Entrypoint   []string
	User         string // uid:gid
	Env          []string
	Cmd          []string
	Labels       map[string]string
	Privileged   bool
	Resources    ResourcesRequest
	Mounts       []mount.Mount
	Networks     []NetworkAttachment
	Timeout      time.Duration
	HealthCheck  *container.HealthConfig
	Contents     []*Content
	PortBindings nat.PortMap
}

type ResourcesRequest struct {
	CpuRequest resource.Quantity
	CpuLimit   resource.Quantity

	MemoryRequest resource.Quantity
	MemoryLimit   resource.Quantity
}

func New(opts ...Option) (*docker, error) {
	d := &docker{
		copts: make([]client.Opt, 0),
	}

	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}

	if d.cli == nil {
		copts := []client.Opt{
			client.FromEnv,
			client.WithAPIVersionNegotiation(),
			client.WithVersionFromEnv(),
		}
		copts = append(copts, d.copts...)

		cli, err := client.NewClientWithOpts(copts...)
		if err != nil {
			return nil, fmt.Errorf("creating docker client: %w", err)
		}
		d.cli = cli
	}

	return d, nil
}

// Start starts a container with the given request.
func (d *docker) Start(ctx context.Context, req *Request) (*Response, error) {
	endpointSettings := make(map[string]*network.EndpointSettings)
	for _, nw := range req.Networks {
		endpointSettings[nw.Name] = &network.EndpointSettings{
			NetworkID: nw.ID,
		}
	}

	if req.Labels == nil {
		req.Labels = make(map[string]string)
	}

	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}

	if req.Ref == nil {
		return nil, fmt.Errorf("no image reference provided")
	}

	if req.PortBindings == nil {
		req.PortBindings = make(nat.PortMap)
	}

	exposedPorts := make(nat.PortSet)
	for port := range req.PortBindings {
		exposedPorts[port] = struct{}{}
	}

	// Pull the image if it doesn't already exist
	if err := d.pull(ctx, req.Ref); err != nil {
		return nil, fmt.Errorf("pulling image: %w", err)
	}

	cresp, err := d.cli.ContainerCreate(ctx,
		&container.Config{
			Image:        req.Ref.String(),
			Entrypoint:   req.Entrypoint,
			User:         req.User,
			Env:          req.Env,
			Cmd:          req.Cmd,
			AttachStdout: true,
			AttachStderr: true,
			Labels:       d.withDefaultLabels(req.Labels),
			Healthcheck:  req.HealthCheck,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Privileged: req.Privileged,
			RestartPolicy: container.RestartPolicy{
				// Never restart
				Name: container.RestartPolicyDisabled,
			},
			Resources: container.Resources{
				Memory:            req.Resources.MemoryLimit.Value(),
				MemoryReservation: req.Resources.MemoryRequest.Value(),
				// mirroring what's done in Docker CLI: https://github.com/docker/cli/blob/0ad1d55b02910f4b40462c0d01aac2934eb0f061/cli/command/container/update.go#L117
				NanoCPUs: req.Resources.CpuRequest.Value(),
			},
			Mounts:       req.Mounts,
			PortBindings: req.PortBindings,
		},
		&network.NetworkingConfig{
			EndpointsConfig: endpointSettings,
		},
		nil, req.Name)
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}

	if cresp.ID == "" {
		return nil, fmt.Errorf("failed to create container, ID is empty")
	}

	for _, content := range req.Contents {
		// `content` is a tar with the full path to the file inside the container,
		// so we always copy to "/"
		if err := d.cli.CopyToContainer(ctx, cresp.ID, "/", content, container.CopyToContainerOptions{}); err != nil {
			return nil, fmt.Errorf("copying content to container: %w", err)
		}
	}

	if err := d.cli.ContainerStart(ctx, cresp.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}

	// Block until the container is running
	cname := ""
	if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, req.Timeout, true, func(ctx context.Context) (bool, error) {
		inspect, err := d.cli.ContainerInspect(ctx, cresp.ID)
		if err != nil {
			// We always want to retry within the timeout, so ignore the error.
			//lint:ignore nilerr reason
			return false, nil
		}

		if !inspect.State.Running {
			return false, nil
		}

		// If there is a health check, block until it is healthy
		if req.HealthCheck != nil {
			if inspect.State.Health == nil {
				return false, nil
			}

			if inspect.State.Health.Status != "healthy" {
				return false, nil
			}
		}

		// Name's start with a leading "/", so remove it
		cname = strings.TrimPrefix(inspect.Name, "/")

		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("waiting for container to be running: %w", err)
	}

	if cname == "" {
		return nil, fmt.Errorf("container name is empty")
	}

	return &Response{
		ID:   cresp.ID,
		Name: cname,
		cli:  d.cli,
	}, nil
}

// Connect returns a response for a container that is already running.
func (d *docker) Connect(ctx context.Context, cid string) (*Response, error) {
	info, err := d.cli.ContainerInspect(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	if !info.State.Running {
		return nil, fmt.Errorf("container is not running")
	}

	return &Response{
		ID:   info.ID,
		Name: info.Name,
		cli:  d.cli,
	}, nil
}

// pull the image if it doesn't exist in the daemon.
func (d *docker) pull(ctx context.Context, ref name.Reference) error {
	if _, _, err := d.cli.ImageInspectWithRaw(ctx, ref.Name()); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("checking if image exists: %w", err)
		}
	}

	// create our own auth token... why this isn't handled by the client is
	// beyond me
	a, err := authn.DefaultKeychain.Resolve(ref.Context().Registry)
	if err != nil {
		return fmt.Errorf("resolving keychain for registry %s: %w", ref.Context().Registry, err)
	}

	acfg, err := a.Authorization()
	if err != nil {
		return fmt.Errorf("getting authorization for registry %s: %w", ref.Context().Registry, err)
	}

	auth := registry.AuthConfig{
		Username: acfg.Username,
		Password: acfg.Password,
		Auth:     acfg.Auth,
	}

	authdata, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("marshaling auth data: %w", err)
	}

	pull, err := d.cli.ImagePull(ctx, ref.Name(), image.PullOptions{
		RegistryAuth: base64.URLEncoding.EncodeToString(authdata),
	})
	if err != nil {
		return err
	}

	// Block until the image is pulled by discarding the reader
	if _, err := io.Copy(io.Discard, pull); err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}

	return nil
}

// Remove forcibly removes all the resources associated with the given request.
func (d *docker) Remove(ctx context.Context, resp *Response) error {
	force := 0
	if err := d.cli.ContainerStop(ctx, resp.ID, container.StopOptions{
		Timeout: &force,
	}); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}

	return d.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{
		RemoveVolumes: true,
	})
}

// Response is returned from a Start() request.
type Response struct {
	ID   string
	Name string
	cli  *client.Client
}

func (r *Response) Run(ctx context.Context, cmd harness.Command) error {
	resp, err := r.cli.ContainerExecCreate(ctx, r.ID, container.ExecOptions{
		Cmd:          []string{"sh", "-c", cmd.Args},
		WorkingDir:   cmd.WorkingDir,
		AttachStderr: true,
		AttachStdout: true,
	})
	if err != nil {
		return fmt.Errorf("creating exec: %w", err)
	}

	if resp.ID == "" {
		return fmt.Errorf("exec ID is empty")
	}

	attach, err := r.cli.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("attaching to exec: %w", err)
	}
	defer attach.Close()

	if err := r.cli.ContainerExecStart(ctx, resp.ID, container.ExecStartOptions{}); err != nil {
		return fmt.Errorf("starting exec: %w", err)
	}

	var stdout, stderr bytes.Buffer
	var stdoutw, stderrw io.Writer
	stdoutw, stderrw = &stdout, &stderr

	if cmd.Stdout != nil {
		stdoutw = io.MultiWriter(stdoutw, cmd.Stdout)
	}

	if cmd.Stderr != nil {
		stderrw = io.MultiWriter(stderrw, cmd.Stderr)
	}

	done := make(chan error, 1)

	go func() {
		_, err := stdcopy.StdCopy(stdoutw, stderrw, attach.Reader)
		done <- err
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("context cancelled while waiting for command to finish: %w", ctx.Err())
	case err := <-done:
		if err != nil {
			return fmt.Errorf("command exited with error: %w", err)
		}
	}

	exec, err := r.cli.ContainerExecInspect(ctx, resp.ID)
	if err != nil {
		return fmt.Errorf("inspecting exec: %w", err)
	}

	if exec.ExitCode != 0 {
		return fmt.Errorf("command exited with non-zero exit code: %d\n\n%s", exec.ExitCode, stderr.String())
	}

	return nil
}

func (d *docker) withDefaultLabels(labels map[string]string) map[string]string {
	l := map[string]string{
		"dev.chainguard.imagetest": "true",
	}

	for k, v := range l {
		if _, ok := labels[k]; !ok {
			labels[k] = v
		}
	}

	return labels
}
