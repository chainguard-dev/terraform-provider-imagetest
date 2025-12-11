package ec2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/docker/cli/cli/connhelper"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
)

func (d *driver) dockerClient(ctx context.Context) (*client.Client, error) {
	log := clog.FromContext(ctx)

	host := net.JoinHostPort(d.instanceIP(), strconv.Itoa(int(d.cfg.SSHPort)))
	url := fmt.Sprintf("ssh://%s", host)

	opts := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=10",
		"-i", d.sshKeyPath(),
		"-l", d.cfg.SSHUser,
	}

	helper, err := connhelper.GetConnectionHelperWithSSHOpts(url, opts)
	if err != nil {
		return nil, fmt.Errorf("creating SSH connection helper: %w", err)
	}

	cli, err := client.NewClientWithOpts(
		client.WithHTTPClient(&http.Client{
			Transport: &http.Transport{DialContext: helper.Dialer},
		}),
		client.WithHost(url),
		client.WithAPIVersionNegotiation(),
		client.WithDialContext(helper.Dialer),
	)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	log.Info("created Docker SSH client", "target", url)
	return cli, nil
}

func (d *driver) pullImage(ctx context.Context, cli *client.Client, ref name.Reference) error {
	auth, err := authn.DefaultKeychain.Resolve(ref.Context().Registry)
	if err != nil {
		return fmt.Errorf("resolving registry auth: %w", err)
	}

	authConfig, err := auth.Authorization()
	if err != nil {
		return fmt.Errorf("getting auth config: %w", err)
	}

	authJSON, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("marshaling auth config: %w", err)
	}
	authStr := base64.URLEncoding.EncodeToString(authJSON)

	result, err := cli.ImagePull(ctx, ref.Name(), image.PullOptions{RegistryAuth: authStr})
	if err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	defer result.Close()

	if _, err := io.Copy(io.Discard, result); err != nil {
		return fmt.Errorf("reading pull response: %w", err)
	}

	return nil
}

func (d *driver) runContainer(ctx context.Context, cli *client.Client, ref name.Reference) (*drivers.RunResult, error) {
	log := clog.FromContext(ctx)

	env := make([]string, 0, len(d.cfg.Env))
	for k, v := range d.cfg.Env {
		env = append(env, k+"="+v)
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
		Mounts:        d.mounts(ctx),
		Resources:     container.Resources{Devices: d.deviceMappings(ctx)},
	}
	hostConfig.DeviceRequests = d.deviceRequests()

	containerName := d.name + "-test"
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:        ref.String(),
			User:         "0:0",
			Env:          env,
			AttachStdout: true,
			AttachStderr: true,
			Healthcheck: &v1.HealthcheckConfig{
				Test:        append([]string{"CMD"}, entrypoint.DefaultHealthCheckCommand...),
				Interval:    1 * time.Second,
				Timeout:     5 * time.Second,
				Retries:     1,
				StartPeriod: 1 * time.Second,
			},
		}, hostConfig, nil, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}

	log.Info("created container", "id", resp.ID)

	// Register cleanup immediately so container is removed even if we fail below
	if err := d.stack.Add(func(ctx context.Context) error {
		log.Info("removing container", "id", resp.ID)
		return cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
	}); err != nil {
		return nil, fmt.Errorf("adding container cleanup to stack: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}

	// Create cancellable context for background goroutines
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	statusCh, errCh := cli.ContainerWait(runCtx, resp.ID, container.WaitConditionNotRunning)

	logs, err := cli.ContainerLogs(runCtx, resp.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching container logs: %w", err)
	}

	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		defer logs.Close()
		buf := make([]byte, 4096)
		for {
			select {
			case <-runCtx.Done():
				return
			default:
				n, err := logs.Read(buf)
				if n > 0 {
					log.Info("container output", "output", string(buf[:n]))
				}
				if err != nil {
					return
				}
			}
		}
	}()

	// Poll health check to detect paused state (exit code 78)
	// When IMAGETEST_SKIP_TEARDOWN is set, the container pauses after success
	// and the health check reports unhealthy with exit code 78
	healthCh := make(chan int64, 1)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				inspect, err := cli.ContainerInspect(runCtx, resp.ID)
				if err != nil || inspect.State == nil || !inspect.State.Running {
					continue
				}
				if inspect.State.Health == nil || inspect.State.Health.Status != "unhealthy" {
					continue
				}
				if len(inspect.State.Health.Log) == 0 {
					continue
				}
				check := inspect.State.Health.Log[len(inspect.State.Health.Log)-1]
				healthCh <- int64(check.ExitCode)
				return
			}
		}
	}()

	var result *drivers.RunResult
	var runErr error

	select {
	case err := <-errCh:
		if err != nil {
			runErr = fmt.Errorf("waiting for container: %w", err)
		}
	case status := <-statusCh:
		switch status.StatusCode {
		case 0, int64(entrypoint.ProcessPausedCode):
			result = &drivers.RunResult{}
		default:
			runErr = fmt.Errorf("container exited with code %d", status.StatusCode)
		}
	case exitCode := <-healthCh:
		// Health check detected a terminal state
		if exitCode == int64(entrypoint.ProcessPausedCode) {
			log.Info("container paused after success (IMAGETEST_SKIP_TEARDOWN)")
			result = &drivers.RunResult{}
		} else {
			runErr = fmt.Errorf("container health check failed with exit code %d", exitCode)
		}
	case <-ctx.Done():
		runErr = ctx.Err()
	}

	// Cancel background goroutines and wait for log streaming to finish
	cancelRun()
	<-logDone

	return result, runErr
}

func (d *driver) mounts(ctx context.Context) []mount.Mount {
	if len(d.cfg.VolumeMounts) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)
	mounts := make([]mount.Mount, 0, len(d.cfg.VolumeMounts))
	for _, vol := range d.cfg.VolumeMounts {
		src, dst, ok := strings.Cut(vol, ":")
		if !ok {
			log.Warn("ignoring malformed volume mount (expected src:dst)", "mount", vol)
			continue
		}
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: src,
			Target: dst,
		})
	}
	return mounts
}

func (d *driver) deviceMappings(ctx context.Context) []container.DeviceMapping {
	if len(d.cfg.DeviceMounts) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)
	devices := make([]container.DeviceMapping, 0, len(d.cfg.DeviceMounts))
	for _, dev := range d.cfg.DeviceMounts {
		src, dst, ok := strings.Cut(dev, ":")
		if !ok {
			log.Warn("ignoring malformed device mount (expected src:dst)", "device", dev)
			continue
		}
		devices = append(devices, container.DeviceMapping{
			PathOnHost:      src,
			PathInContainer: dst,
		})
	}
	return devices
}

func (d *driver) deviceRequests() []container.DeviceRequest {
	if d.cfg.GPUs == "" {
		return nil
	}

	count := -1 // "all" GPUs
	if d.cfg.GPUs != "all" {
		if n, err := strconv.Atoi(d.cfg.GPUs); err == nil {
			count = n
		}
	}

	return []container.DeviceRequest{{
		Driver:       "nvidia",
		Count:        count,
		Capabilities: [][]string{{"gpu"}},
	}}
}
