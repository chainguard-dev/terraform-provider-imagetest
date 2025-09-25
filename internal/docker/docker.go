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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/cli/cli/connhelper"
	sshhelper "github.com/docker/cli/cli/connhelper/ssh"
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

var defaultSSHArgs = []string{"-o", "StrictHostKeyChecking=no"}

type Client struct {
	inner     *client.Client
	copts     []client.Opt
	sshConfig *sshhelper.Spec
}

func (c *Client) IsSSH() bool {
	return c.sshConfig != nil
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
	CgroupnsMode container.CgroupnsMode
	PidMode      string
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

	if d.inner == nil {
		copts := []client.Opt{
			client.WithAPIVersionNegotiation(),
			client.WithVersionFromEnv(),
			client.WithTLSClientConfigFromEnv(),
		}
		if hostOverride := os.Getenv("IMAGETEST_DOCKER_HOST"); hostOverride != "" {
			if strings.HasPrefix(hostOverride, "ssh://") {
				spec, err := sshhelper.ParseURL(hostOverride)
				if err != nil {
					return nil, fmt.Errorf("parsing SSH URL: %w", err)
				}
				d.sshConfig = spec

				helper, err := connhelper.GetConnectionHelperWithSSHOpts(hostOverride, defaultSSHArgs)
				if err != nil {
					return nil, fmt.Errorf("creating docker SSH connection helper: %w", err)
				}
				httpClient := &http.Client{
					Transport: &http.Transport{
						DialContext: helper.Dialer,
					},
				}
				copts = append(copts, client.WithHTTPClient(httpClient))
			} else {
				copts = append(copts, client.WithHost(hostOverride))
			}
		} else {
			copts = append(copts, client.WithHostFromEnv())
		}
		copts = append(copts, d.copts...)

		cli, err := client.NewClientWithOpts(copts...)
		if err != nil {
			return nil, fmt.Errorf("creating docker client: %w", err)
		}
		d.inner = cli
	}
	return d, nil
}

func (d *Client) Run(ctx context.Context, req *Request) (string, error) {
	cid, err := d.start(ctx, req)
	if err != nil {
		return "", fmt.Errorf("starting container: %w", err)
	}

	statusCh, errCh := d.inner.ContainerWait(ctx, cid, container.WaitConditionNotRunning)

	// TODO: This is specific to Run() and not in Start() because Run() has a
	// clearly defined exit condition. In the future we may want to consider
	// adding this to Start(), but its unclear how useful those logs would be,
	// and how to even surface them without being overly verbose.
	if req.Logger != nil {
		logs, err := d.inner.ContainerLogs(ctx, cid, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
		})
		if err != nil {
			return "", fmt.Errorf("failed to get logs: %w", err)
		}
		defer logs.Close()

		go func() {
			defer logs.Close()
			_, err = stdcopy.StdCopy(req.Logger, req.Logger, logs)
			if err != nil {
				_, _ = fmt.Fprintf(req.Logger, "error copying logs: %v", err)
			}
		}()
	}

	// If a health check is present, set up a poller to poll on health status
	unhealthyCh := make(chan error)
	if req.HealthCheck != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					inspect, err := d.inner.ContainerInspect(ctx, cid)
					if err != nil {
						unhealthyCh <- fmt.Errorf("inspecting container: %w", err)
						return
					}

					if inspect.State != nil && inspect.State.Health != nil {
						if inspect.State.Health.Status == "unhealthy" {
							check := inspect.State.Health.Log[len(inspect.State.Health.Log)-1]
							unhealthyCh <- &RunError{
								ExitCode: int64(check.ExitCode),
								Message:  check.Output,
							}
							return
						}
					}
					time.Sleep(time.Second)
				}
			}
		}()
	}

	select {
	case <-ctx.Done():
		return cid, fmt.Errorf("context cancelled while waiting for container to exit: %w", ctx.Err())

	case err := <-errCh:
		return cid, fmt.Errorf("waiting for container to exit: %w", err)

	case status := <-statusCh:
		if status.Error != nil {
			return cid, &RunError{
				ExitCode: status.StatusCode,
				Message:  status.Error.Message,
			}
		}

		if status.StatusCode != 0 {
			return cid, &RunError{ExitCode: status.StatusCode}
		}

	case err := <-unhealthyCh:
		return cid, err
	}

	return cid, nil
}

type RunError struct {
	ExitCode int64
	Message  string
}

func (e *RunError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("container exited with non-zero exit code: %d", e.ExitCode)
	}
	return fmt.Sprintf("container exited with non-zero exit code: %d: %s", e.ExitCode, e.Message)
}

// Start starts a container with the given request.
func (d *Client) Start(ctx context.Context, req *Request) (*Response, error) {
	cid, err := d.start(ctx, req)
	if err != nil {
		return nil, err
	}

	// Block until the container is running
	cname := ""
	var cjson container.InspectResponse
	if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, req.Timeout, true, func(ctx context.Context) (bool, error) {
		inspect, err := d.inner.ContainerInspect(ctx, cid)
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
		cli:           d,
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

	cresp, err := d.inner.ContainerCreate(ctx,
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
		if err := d.inner.CopyToContainer(ctx, cresp.ID, "/", content, container.CopyToContainerOptions{}); err != nil {
			return "", fmt.Errorf("copying content to container: %w", err)
		}
	}

	if err := d.inner.ContainerStart(ctx, cresp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("starting container: %w", err)
	}

	return cresp.ID, nil
}

// Connect returns a response for a container that is already running.
func (d *Client) Connect(ctx context.Context, cid string) (*Response, error) {
	info, err := d.inner.ContainerInspect(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	if !info.State.Running {
		return nil, fmt.Errorf("container is not running")
	}

	return &Response{
		ID:   info.ID,
		Name: info.Name,
		cli:  d,
	}, nil
}

// pull the image if it doesn't exist in the daemon.
func (d *Client) pull(ctx context.Context, ref name.Reference) error {
	var buf bytes.Buffer
	if _, err := d.inner.ImageInspect(ctx, ref.Name(), client.ImageInspectWithRawResponse(&buf)); err != nil {
		if !cerrdefs.IsNotFound(err) {
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

	pull, err := d.inner.ImagePull(ctx, ref.Name(), image.PullOptions{
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
	if err := d.inner.ContainerStop(ctx, resp.ID, container.StopOptions{
		Timeout: &force,
	}); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}

	return d.inner.ContainerRemove(ctx, resp.ID, container.RemoveOptions{
		RemoveVolumes: true,
	})
}

// Response is returned from a Start() request.
type Response struct {
	types.ContainerJSON
	ID   string
	Name string
	cli  *Client
}

// PortBinding returns the host port binding for a container port.
// For SSH connections, it creates a tunnel and returns a cleanup function.
// For local connections, it returns the local port and cleanup is a no-op.
func (r *Response) PortBinding(port nat.Port) (nat.PortBinding, func(), error) {
	bindings, ok := r.NetworkSettings.Ports[port]
	if !ok || len(bindings) == 0 {
		return nat.PortBinding{}, nil, fmt.Errorf("port %s not exposed by container", port)
	}

	if !r.cli.IsSSH() {
		return bindings[0], func() {}, nil
	}

	// For SSH, tunnel to the port on the remote Docker host
	// The container's port is already mapped to the host
	remoteBinding := bindings[0]
	remotePort, err := strconv.Atoi(remoteBinding.HostPort)
	if err != nil {
		return nat.PortBinding{}, nil, fmt.Errorf("parsing remote port: %w", err)
	}

	// Tunnel from the remote host's mapped port to a local port
	localPort, cleanup, err := r.cli.tunnelToPort("127.0.0.1", remotePort)
	if err != nil {
		return nat.PortBinding{}, nil, fmt.Errorf("creating tunnel: %w", err)
	}

	return nat.PortBinding{
		HostIP:   "127.0.0.1",
		HostPort: localPort,
	}, cleanup, nil
}

func (r *Response) Run(ctx context.Context, cmd harness.Command) error {
	resp, err := r.cli.inner.ContainerExecCreate(ctx, r.ID, container.ExecOptions{
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

	attach, err := r.cli.inner.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("attaching to exec: %w", err)
	}
	defer attach.Close()

	if err := r.cli.inner.ContainerExecStart(ctx, resp.ID, container.ExecStartOptions{}); err != nil {
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

	exec, err := r.cli.inner.ContainerExecInspect(ctx, resp.ID)
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

func GetFile(ctx context.Context, cli *Client, cid string, path string) (io.ReadCloser, error) {
	// ensure path is absolute
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path %s is not absolute", path)
	}

	trc, _, err := cli.inner.CopyFromContainer(ctx, cid, path)
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

	return &fileReader{
		tr:  tr,
		trc: trc,
	}, nil
}

type fileReader struct {
	tr  *tar.Reader
	trc io.ReadCloser
}

func (fr *fileReader) Read(p []byte) (int, error) {
	return fr.tr.Read(p)
}

func (fr *fileReader) Close() error {
	return fr.trc.Close()
}

func (r *Response) GetFile(ctx context.Context, path string) (io.Reader, error) {
	return GetFile(ctx, r.cli, r.ID, path)
}

// ReadFile is a helper method over GetFile() that returns the raw contents.
func (r *Response) ReadFile(ctx context.Context, path string) ([]byte, error) {
	rdr, err := r.GetFile(ctx, path)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(rdr)
	if err != nil {
		return nil, err
	}

	return data, nil
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

// DockerConfig is the structure of the config file used at
// ~/.docker/config.json.
type DockerConfig struct {
	Auths map[string]DockerAuthConfig `json:"auths,omitempty"`
}

type DockerAuthConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

func (d *DockerConfig) Content() (*Content, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	return NewContentFromString(string(data), "/root/.docker/config.json"), nil
}

func (c *Client) tunnelToPort(remoteHost string, remotePort int) (string, func(), error) {
	if !c.IsSSH() {
		return "", nil, fmt.Errorf("not an SSH connection")
	}

	// Find an available local port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("finding available port: %w", err)
	}
	localAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return "", nil, fmt.Errorf("unexpected address type: %T", listener.Addr())
	}
	localPort := localAddr.Port
	// Close so SSH can bind to it
	if err := listener.Close(); err != nil {
		return "", nil, fmt.Errorf("closing port: %w", err)
	}

	sshArgs := append(defaultSSHArgs,
		"-N", // No command execution
		"-L", fmt.Sprintf("%d:%s:%d", localPort, remoteHost, remotePort),
	)

	specArgs := c.sshConfig.Args()
	if specArgs == nil {
		return "", nil, fmt.Errorf("unable to build SSH arguments")
	}
	sshArgs = append(sshArgs, specArgs...)

	cmd := exec.Command("ssh", sshArgs...)
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("starting SSH tunnel: %w", err)
	}

	cleanup := func() {
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				clog.Warnf("failed to kill SSH process: %v", err)
			}
			if err := cmd.Wait(); err != nil {
				clog.Warnf("failed to wait for SSH process: %v", err)
			}
		}
	}

	// Wait for the tunnel to be ready
	for range 50 {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 100*time.Millisecond)
		if err == nil {
			if err := conn.Close(); err != nil {
				clog.Warnf("failed to close test connection: %v", err)
			}
			return fmt.Sprintf("%d", localPort), cleanup, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	cleanup()
	return "", nil, fmt.Errorf("tunnel failed to establish after 5 seconds")
}
