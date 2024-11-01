// docker holds useful things for interacting with docker
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
	"strings"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/docker/docker/api/types"
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

type Client struct {
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
	ExtraHosts   []string
	AutoRemove   bool
	Logger       io.Writer
	Init         bool
}

type ResourcesRequest struct {
	CpuRequest resource.Quantity
	CpuLimit   resource.Quantity

	MemoryRequest resource.Quantity
	MemoryLimit   resource.Quantity
}

func New(opts ...Option) (*Client, error) {
	d := &Client{
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

func (d *Client) Run(ctx context.Context, req *Request) (string, error) {
	req.AutoRemove = true
	cid, err := d.start(ctx, req)
	if err != nil {
		return "", fmt.Errorf("starting container: %w", err)
	}

	statusCh, errCh := d.cli.ContainerWait(ctx, cid, container.WaitConditionNotRunning)

	// TODO: This is specific to Run() and not in Start() because Run() has a
	// clearly defined exit condition. In the future we may want to consider
	// adding this to Start(), but its unclear how useful those logs would be,
	// and how to even surface them without being overly verbose.
	if req.Logger != nil {
		defer func() {
			logs, err := d.cli.ContainerLogs(ctx, cid, container.LogsOptions{
				ShowStdout: true,
				ShowStderr: true,
				Follow:     true,
			})
			if err != nil {
				fmt.Fprintf(req.Logger, "failed to get logs: %v\n", err)
				return
			}
			defer logs.Close()

			_, err = stdcopy.StdCopy(req.Logger, req.Logger, logs)
			if err != nil {
				fmt.Fprintf(req.Logger, "error copying logs: %v", err)
			}
		}()
	}

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context cancelled while waiting for container to exit: %w", ctx.Err())

	case err := <-errCh:
		return "", fmt.Errorf("waiting for container to exit: %w", err)

	case status := <-statusCh:
		if status.Error != nil {
			return "", fmt.Errorf("container exited with error: %s", status.Error.Message)
		}

		if status.StatusCode != 0 {
			return "", fmt.Errorf("container exited with non-zero status code: %d", status.StatusCode)
		}
	}

	return cid, nil
}

// Start starts a container with the given request.
func (d *Client) Start(ctx context.Context, req *Request) (*Response, error) {
	cid, err := d.start(ctx, req)
	if err != nil {
		return nil, err
	}

	// Block until the container is running
	cname := ""
	var cjson types.ContainerJSON
	if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, req.Timeout, true, func(ctx context.Context) (bool, error) {
		inspect, err := d.cli.ContainerInspect(ctx, cid)
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

		cjson = inspect

		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("waiting for container to be running: %w", err)
	}

	if cname == "" {
		return nil, fmt.Errorf("container name is empty")
	}

	return &Response{
		ContainerJSON: cjson,
		ID:            cid,
		Name:          cname,
		cli:           d.cli,
	}, nil
}

// start will create and start a container, returning the container ID or
// error. It should be called by public methods that need to start a container
// (like Start() or Run()).
func (d *Client) start(ctx context.Context, req *Request) (string, error) {
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
		return "", fmt.Errorf("no image reference provided")
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
		return "", fmt.Errorf("pulling image: %w", err)
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
			ExtraHosts: req.ExtraHosts,
			Privileged: req.Privileged,
			RestartPolicy: container.RestartPolicy{
				// Never restart
				Name: container.RestartPolicyDisabled,
			},
			Resources: container.Resources{
				Memory:            req.Resources.MemoryLimit.Value(),
				MemoryReservation: req.Resources.MemoryRequest.Value(),
				NanoCPUs:          req.Resources.CpuRequest.Value(),
			},
			Mounts:       req.Mounts,
			PortBindings: req.PortBindings,
			AutoRemove:   req.AutoRemove,
			Init:         &req.Init,
		},
		&network.NetworkingConfig{
			EndpointsConfig: endpointSettings,
		},
		nil, req.Name)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	if cresp.ID == "" {
		return "", fmt.Errorf("failed to create container, ID is empty")
	}

	for _, content := range req.Contents {
		if err := d.cli.CopyToContainer(ctx, cresp.ID, "/", content, container.CopyToContainerOptions{}); err != nil {
			return "", fmt.Errorf("copying content to container: %w", err)
		}
	}

	if err := d.cli.ContainerStart(ctx, cresp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("starting container: %w", err)
	}

	return cresp.ID, nil
}

// Connect returns a response for a container that is already running.
func (d *Client) Connect(ctx context.Context, cid string) (*Response, error) {
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
func (d *Client) pull(ctx context.Context, ref name.Reference) error {
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
func (d *Client) Remove(ctx context.Context, resp *Response) error {
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
	types.ContainerJSON
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

	var stdout, stderr, stdall bytes.Buffer
	var stdoutw, stderrw, stdallw io.Writer
	stdoutw, stderrw, stdallw = &stdout, &stderr, &stdall

	if cmd.Stdout != nil {
		stdoutw = io.MultiWriter(stdoutw, stdallw, cmd.Stdout)
	}

	if cmd.Stderr != nil {
		stderrw = io.MultiWriter(stderrw, stdallw, cmd.Stderr)
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
		return &harness.RunError{
			ExitCode:       exec.ExitCode,
			CombinedOutput: stdall.String(),
			Cmd:            cmd.Args,
		}
	}

	return nil
}

func (r *Response) GetFile(ctx context.Context, path string) (io.Reader, error) {
	// ensure path is absolute
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path %s is not absolute", path)
	}

	trc, _, err := r.cli.CopyFromContainer(ctx, r.ID, path)
	if err != nil {
		return nil, err
	}

	// its a tar archive and we just want to return the files read closer
	tr := tar.NewReader(trc)
	hdr, err := tr.Next()
	if err == io.EOF {
		return nil, fmt.Errorf("no file found in tar")
	} else if err != nil {
		return nil, err
	}

	if hdr.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}

	if hdr.Name != filepath.Base(path) {
		return nil, fmt.Errorf("requested file %s does not match what is in the archive: %s", hdr.Name, path)
	}

	return tr, nil
}

func (d *Client) withDefaultLabels(labels map[string]string) map[string]string {
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
