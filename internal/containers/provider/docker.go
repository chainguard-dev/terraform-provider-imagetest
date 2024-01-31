package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const DockerProviderName = "docker"

type DockerProvider struct {
	cli *client.Client
	// id is the ID of the running container it is run
	id string
	// name is the name to use for the container
	name string
	req  DockerRequest
}

type DockerRequest struct {
	ContainerRequest
	Mounts []mount.Mount
}

func NewDocker(name string, req DockerRequest) (*DockerProvider, error) {
	cli, err := client.NewClientWithOpts()
	if err != nil {
		return nil, err
	}

	return &DockerProvider{
		name: name,
		req:  req,
		cli:  cli,
	}, nil
}

// Start implements Provider.
func (p *DockerProvider) Start(ctx context.Context) error {
	if err := p.pull(ctx, p.req.Image); err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}

	mode, err := p.cli.NetworkCreate(ctx, p.name, types.NetworkCreate{
		Attachable: true,
		Driver:     "bridge",
	})
	if err != nil {
		return fmt.Errorf("creating network: %w", err)
	}

	config := &container.Config{
		Image:        p.req.Image,
		User:         p.req.User,
		Env:          p.req.Env.ToSlice(),
		Entrypoint:   p.req.Entrypoint,
		Cmd:          p.req.Cmd,
		AttachStdout: true,
		AttachStderr: true,
		Labels: map[string]string{
			"imagetest": "true",
		},
	}

	hostConfig := &container.HostConfig{
		NetworkMode: container.NetworkMode(mode.ID),
		Mounts:      p.req.Mounts,
		Privileged:  p.req.Privileged,
		RestartPolicy: container.RestartPolicy{
			Name: "no",
		},
		Resources: container.Resources{
			MemoryReservation: p.req.Resources.MemoryRequest.Value(),
			Memory:            p.req.Resources.CpuLimit.Value(),
		},
	}

	resp, err := p.cli.ContainerCreate(ctx, config, hostConfig, nil, nil, p.name)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	p.id = resp.ID

	for _, file := range p.req.Files {
		tarfile, err := file.tar()
		if err != nil {
			return fmt.Errorf("creating tar file: %w", err)
		}

		dir := filepath.Dir(file.Target)
		if err := p.cli.CopyToContainer(ctx, p.id, dir, tarfile, types.CopyToContainerOptions{}); err != nil {
			return fmt.Errorf("copying file to container: %w", err)
		}
	}

	if err := p.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	for _, id := range p.req.Networks {
		networkResource, err := p.cli.NetworkInspect(ctx, id, types.NetworkInspectOptions{})
		if err != nil {
			return fmt.Errorf("unknown network %s: %w", id, err)
		}

		if err := p.cli.NetworkConnect(ctx, networkResource.ID, resp.ID, &network.EndpointSettings{}); err != nil {
			return fmt.Errorf("connecting container to user defined network: %w", err)
		}
	}

	return nil
}

// Teardown implements Provider.
func (p *DockerProvider) Teardown(ctx context.Context) error {
	var errs []error

	timeout := 0
	if err := p.cli.ContainerStop(ctx, p.id, container.StopOptions{
		Timeout: &timeout,
	}); err != nil {
		errs = append(errs, fmt.Errorf("stopping container %s: %w", p.id, err))
	}

	if err := p.cli.ContainerRemove(ctx, p.id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	}); err != nil {
		errs = append(errs, fmt.Errorf("removing container %s: %w", p.id, err))
	}

	networkResource, err := p.cli.NetworkInspect(ctx, p.name, types.NetworkInspectOptions{})
	if err == nil {
		if err := p.cli.NetworkRemove(ctx, networkResource.ID); err != nil {
			errs = append(errs, fmt.Errorf("removing network: %w", err))
		}
	}

	if len(errs) > 0 {
		var err error
		for _, e := range errs {
			if err != nil {
				err = fmt.Errorf("%w: %v", err, e)
			} else {
				err = e
			}
		}
		return err
	}

	return nil
}

// Exec implements Provider.
func (p *DockerProvider) Exec(ctx context.Context, config ExecConfig) (io.Reader, error) {
	execConfig := types.ExecConfig{
		Cmd:          []string{"/bin/sh", "-c", config.Command},
		WorkingDir:   config.WorkingDir,
		AttachStderr: true,
		AttachStdout: true,
	}

	resp, err := p.cli.ContainerExecCreate(ctx, p.id, execConfig)
	if err != nil {
		return nil, err
	}

	check := types.ExecStartCheck{}
	attach, err := p.cli.ContainerExecAttach(ctx, resp.ID, check)
	if err != nil {
		return nil, err
	}

	doneChan := make(chan struct{})
	defer close(doneChan)

	// listen for context cancellation that signals the exec attachment should be
	// closed, which triggers stdcopy.StdCopy to finish/fail
	go func() {
		select {
		case <-ctx.Done():
			attach.Close()
		case <-doneChan:
		}
	}()

	if err := p.cli.ContainerExecStart(ctx, resp.ID, check); err != nil {
		return nil, err
	}

	out := &bytes.Buffer{}
	if _, err := stdcopy.StdCopy(out, out, attach.Conn); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("timed out waiting for command to finish: %w", ctx.Err())
		}
		return nil, err
	}
	doneChan <- struct{}{}

	var exitCode int

	// poll the exec status until it is stopped running
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		exec, err := p.cli.ContainerExecInspect(ctx, resp.ID)
		if err != nil {
			return nil, err
		}

		if !exec.Running {
			exitCode = exec.ExitCode
			if exitCode != 0 {
				return nil, fmt.Errorf("command exited with non-zero exit code: %d\n\n%s", exitCode, out.String())
			}
			break
		}
	}

	return out, nil
}

// pull the image if it doesn't exist in the daemon
// TODO: Do this with ggcr.
func (p *DockerProvider) pull(ctx context.Context, imageId string) error {
	// check if the imageId exists in the daemon
	_, _, err := p.cli.ImageInspectWithRaw(ctx, imageId)
	if err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("checking if image exists: %w", err)
		}
	}

	// pull the image if it doesn't exist
	pull, err := p.cli.ImagePull(ctx, imageId, types.ImagePullOptions{})
	if err != nil {
		return err
	}

	_, err = io.ReadAll(pull)
	return err
}
