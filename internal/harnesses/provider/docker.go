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
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const DockerProviderName = "docker"

type DockerProvider struct {
	client *client.Client
	// id is the ID of the container after it is run
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
		name:   name,
		req:    req,
		client: cli,
	}, nil
}

// Start implements Provider.
func (p *DockerProvider) Start(ctx context.Context) error {
	mode, err := p.client.NetworkCreate(ctx, p.name, types.NetworkCreate{})
	if err != nil {
		return fmt.Errorf("creating network: %w", err)
	}

	resp, err := p.client.ContainerCreate(ctx, &container.Config{
		Image:        p.req.Image,
		User:         p.req.User,
		Env:          p.req.Env.ToSlice(),
		Entrypoint:   p.req.Entrypoint,
		Cmd:          p.req.Cmd,
		AttachStdout: true,
		AttachStderr: true,
	}, &container.HostConfig{
		NetworkMode: container.NetworkMode(mode.ID),
		Mounts:      p.req.Mounts,
		Privileged:  p.req.Privileged,
	}, nil, nil, p.name)
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
		if err := p.client.CopyToContainer(ctx, p.id, dir, tarfile, types.CopyToContainerOptions{}); err != nil {
			return fmt.Errorf("copying file to container: %w", err)
		}
	}

	if err := p.client.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	for _, network := range p.req.Networks {
		err := p.client.NetworkConnect(ctx, network, resp.ID, nil)
		if err != nil {
			return fmt.Errorf("connecting container to user defined network: %w", err)
		}
	}

	return nil
}

// Teardown implements Provider.
func (p *DockerProvider) Teardown(ctx context.Context) error {
	var errs []error

	timeout := 0
	if err := p.client.ContainerStop(ctx, p.id, container.StopOptions{
		Timeout: &timeout,
	}); err != nil {
		errs = append(errs, fmt.Errorf("stopping container: %w", err))
	}

	if err := p.client.ContainerRemove(ctx, p.id, types.ContainerRemoveOptions{}); err != nil {
		errs = append(errs, fmt.Errorf("removing container: %w", err))
	}

	if err := p.client.NetworkRemove(ctx, p.name); err != nil {
		errs = append(errs, fmt.Errorf("removing network: %w", err))
	}

	if len(errs) > 0 {
		var err error
		for _, e := range errs {
			err = fmt.Errorf("%w: %v", err, e)
		}
		return err
	}

	return nil
}

// Exec implements Provider.
func (p *DockerProvider) Exec(ctx context.Context, command string) (io.Reader, error) {
	resp, err := p.client.ContainerExecCreate(ctx, p.id, types.ExecConfig{
		Cmd:          []string{"/bin/sh", "-c", command},
		AttachStderr: true,
		AttachStdout: true,
	})
	if err != nil {
		return nil, err
	}

	check := types.ExecStartCheck{}
	attach, err := p.client.ContainerExecAttach(ctx, resp.ID, check)
	if err != nil {
		return nil, err
	}
	defer attach.Close()

	if err := p.client.ContainerExecStart(ctx, resp.ID, check); err != nil {
		return nil, err
	}

	out := &bytes.Buffer{}
	if _, err := stdcopy.StdCopy(out, out, attach.Reader); err != nil {
		return nil, err
	}

	// Block until the command is done
	var exitCode int
	for {
		exec, err := p.client.ContainerExecInspect(ctx, resp.ID)
		if err != nil {
			return nil, err
		}

		if !exec.Running {
			exitCode = exec.ExitCode
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if exitCode != 0 {
		return nil, fmt.Errorf("command exited with non-zero exit code: %d\n%s", exitCode, out.String())
	}

	return out, nil
}
