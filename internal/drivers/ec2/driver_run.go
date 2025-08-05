package ec2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/docker/cli/cli/connhelper"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
)

// Run accepts a 'name.Reference' of a Docker image, mutated and passed in from
// the 'tests' resource, initializes a Docker SSH client connection to the EC2
// instance constructed in the 'Setup' call, and runs the image on that host.
//
// Run implements 'drivers.Tester'.
func (d *Driver) Run(ctx context.Context, name name.Reference) (*drivers.RunResult, error) {
	log := clog.FromContext(ctx)

	// Construct a Docker client using SSH as the transport.
	client, err := dockerClientSSH(
		ctx,
		d.Exec.User, d.instance.KeyPath, d.net.ElasticIP, portSSH,
	)
	if err != nil {
		return nil, err
	}
	log.Info("Docker SSH client initialization is successful")
	defer func() {
		err := client.Close()
		if err != nil {
			log.Error("encountered error closing Docker client", "error", err)
		} else {
			log.Info("Docker client close is successful")
		}
	}()

	// Prepare the container environment 'key=value' pairs.
	env := make([]string, 0, len(d.Exec.Env))
	for k, v := range d.Exec.Env {
		log.Debug("preparing remote environment variable", "key", k, "value", v)
		env = append(env, k+"="+v)
	}

	// Resolve the auth mechanism for the target registry.
	authConfig, err := resolveAuthForRegistry(ctx, name)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to resolve auth for registry [%s]: %w",
			name.Context().Registry, err,
		)
	}
	log.Info("auth config resolution for target registry is successful")

	// Marshal the auth payload for the target registry.
	authPayload, err := marshalAuthForRegistry(ctx, authConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal registry auth payload: %w", err)
	}
	log.Info("registry auth marshal is successful")

	// Pull the image.
	pullResult, err := client.ImagePull(ctx, name.Name(), image.PullOptions{
		RegistryAuth: authPayload,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}
	defer pullResult.Close()
	// Block until the image is pulled by discarding the reader.
	if _, err := io.Copy(io.Discard, pullResult); err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}
	if err = pullResult.Close(); err != nil {
		// I left this as a warning only because we presumably don't want this
		// closure to halt a test. I don't think it would leak enough to become a
		// problem in CI either but if anyone has strong evidence otherwise, by all
		// means!
		log.Warn("received error in image pull response reader close", "error", err)
	}
	log.Info("Docker image pull is successful", "image", name.String())

	// Prepare the 'HostConfig'.
	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyDisabled,
		},
		Mounts: d.mounts(ctx),
		Resources: container.Resources{
			Devices: d.deviceMappings(ctx),
		},
	}
	// The 'MountAllGPUs' option is equivalent to '--gpus all'.
	if d.MountAllGPUs {
		hostConfig.DeviceRequests = append(hostConfig.DeviceRequests, container.DeviceRequest{
			Driver:       "nvidia",
			Count:        -1,
			DeviceIDs:    nil,
			Capabilities: [][]string{{"gpu"}},
			Options:      make(map[string]string),
		})
	}

	// Run the container.
	log.Info(
		"creating container",
		"name", d.runID,
		"image", name.String(),
	)
	testContainer, err := client.ContainerCreate(ctx,
		&container.Config{
			Image: name.String(),
			// TODO: Could be really nifty to expose the in-container user+group
			// config to the test author.
			User:         "0:0",
			Env:          env,
			AttachStdout: true,
			AttachStderr: true,
			// This HealthCheck is more important than it might first appear.
			//
			// Part of 'imagetest's testing methodology involves a wrapped entrypoint
			// (see: 'cmd/entrypoint'). This wrapper performs any necessary driver-
			// specific environment setup then _waits for a healthcheck probe_. When,
			// and only when, a health check probe is received does it then execute
			// the image test.
			Healthcheck: &v1.HealthcheckConfig{
				Test: append(
					[]string{"CMD"},
					entrypoint.DefaultHealthCheckCommand...,
				),
				Interval:    1 * time.Second,
				Timeout:     5 * time.Second,
				Retries:     1,
				StartPeriod: 1 * time.Second,
			},
		}, hostConfig, nil, nil, d.runID)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}
	log.Info("container launch is successful")

	// Start the container.
	err = client.ContainerStart(ctx, testContainer.ID, container.StartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}
	log.Info("container start is successful")

	// Retrieve container stdout + stderr.
	logs, err := client.ContainerLogs(ctx, testContainer.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Details:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch container logs: %w", err)
	}
	defer logs.Close()

	// Read all logs.
	output, err := io.ReadAll(logs)
	if err != nil {
		return nil, fmt.Errorf("failed to read container launch output: %w", err)
	}
	log.Info("log read complete", "output", string(output))

	// Wait for the container to exit.
	//
	// We'll give it 5-minutes.
	//
	// TODO: @imjasonh @joshrwolf Is this reasonable for the every-case in CI?
	cancellable, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	log.Info("beginning wait for container exit")
	err = waitForContainerExit(cancellable, client, testContainer.ID)
	if err != nil {
		log.Error("hit deadline waiting for container to complete")
		return nil, err
	}
	log.Info("container exit detected")

	// Fetch the container state.
	inspect, err := client.ContainerInspect(ctx, testContainer.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}
	log.Info(
		"post-exit container inspect is successful",
		"status", inspect.State.Status,
		"exit_code", inspect.State.ExitCode,
	)

	// Produce an error on any undesired conditions.
	if inspect.State.ExitCode != 0 {
		return nil, fmt.Errorf(
			"container exited with code: %d",
			inspect.State.ExitCode,
		)
	}
	log.Info("test is complete")

	return nil, nil
}

// sshSaveKey marshals the ED25519 private key to the PEM-encoded OpenSSH format
// and writes it to disk in the standard user '~/.ssh' directory.
func sshSaveKey(_ context.Context, privKey ssh.ED25519PrivateKey, path string) error {
	// Create the SSH directory, if necessary.
	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("failed to create user SSH directory: %w", err)
	}

	// Marshal the generated private pem to the OpenSSH PEM-encoded format.
	pem, err := privKey.MarshalOpenSSH(filepath.Base(path))
	if err != nil {
		return err // No annotation required.
	}

	// Write the private key to disk.
	err = os.WriteFile(path, pem, 0o600)
	if err != nil {
		return fmt.Errorf("failed to write key to disk: %w", err)
	}

	return nil
}

// sshKeyPath constructs a file path to the provided key file name.
func sshKeyPath(keyName string) (string, error) {
	dir, err := sshDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keyName), nil
}

// sshDir produces the SSH directory for the currently logged in user.
//
// NOTE: This path is formed by joining the resolved user home directory to the
// standard '.ssh' directory name. It is up to the caller to ensure this
// directory exists before using it.
func sshDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".ssh"), nil
}

// dockerClientSSH constructs a Docker '*client.Client' with an underlying
// transport of SSH.
func dockerClientSSH(ctx context.Context, user, keyPath, host string, port uint16) (*client.Client, error) {
	log := clog.FromContext(ctx)

	// Construct the full SSH URL.
	//
	// Join the host and port, if we received one. We use this rather than
	// 'fmt.Sprintf' to account for the formatting differences between IPv4 and
	// IPv6.
	if port != 0 {
		host = net.JoinHostPort(host, strconv.Itoa(int(port)))
	}
	target := url.URL{
		Scheme: "ssh",
		User:   url.User(user),
		Host:   host,
	}
	url := target.String()
	log.Info("constructed Docker SSH target", "target", url)

	// Construct a Docker connection helper with an underlying transport of SSH.
	helper, err := connhelper.GetConnectionHelperWithSSHOpts(url, []string{
		// Authenticate to the remote instance with our ED25519 private key.
		"-i", keyPath,
		// Provide the username determined by the config.
		"-l", user,
		// Since we're launching arbitrary AMIs from the AWS marketplace, we don't
		// have host keys that we're aware of ahead of time. In the future, a really
		// nice added security measure could be our own AMIs with host keys we can
		// know to verify ahead of time.
		"-o", "StrictHostKeyChecking=no",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init connection helper: %w", err)
	}
	log.Info("Docker SSH connection helper construction is successful")

	// Construct the Docker 'client.Client'.
	client, err := client.NewClientWithOpts(
		// Construct an HTTPClient where the underlying dialer is our SSH dialer.
		client.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				DialContext: helper.Dialer,
			},
		}),
		// Specify the SSH-scheme URI as the hostname.
		client.WithHost(url),
		client.WithAPIVersionNegotiation(),
		client.WithDialContext(helper.Dialer),
	)
	if err != nil {
		log.Error("failed to init Docker client", "error", err)
		return nil, fmt.Errorf("failed to init Docker client: %w", err)
	}
	log.Info("Docker SSH client construction is successful")

	return client, nil
}

// resolveAuthForRegistry identifies the auth mechanism for the registry defined
// by the provided 'name.Reference' and returns the associated auth mechanism
// as an '*authn.AuthConfig'.
func resolveAuthForRegistry(ctx context.Context, ref name.Reference) (*authn.AuthConfig, error) {
	log := clog.FromContext(ctx)
	// Resolve the keychain for the target registry.
	authn, err := authn.DefaultKeychain.Resolve(ref.Context().Registry)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve keychain: %w", err)
	}
	log.Info(
		"keychain resolution for target registry is successful",
		"registry", ref.Context().Registry,
	)

	// Resolve the targeted registry keychain auth configuration.
	authConfig, err := authn.Authorization()
	if err != nil {
		return nil, fmt.Errorf("failed to get registry authorization: %w", err)
	}
	log.Info(
		"registry auth config retrieval is successful",
		"registry", ref.Context().Registry,
	)

	return authConfig, nil
}

// marshalAuthForRegistry prepares an '*authn.AuthConfig' for use with the
// standard Docker SDK client (ex: 'ImagePull') by JSON-marshaling it and
// encoding the result with base64.
func marshalAuthForRegistry(ctx context.Context, authConfig *authn.AuthConfig) (string, error) {
	log := clog.FromContext(ctx)

	// Marshal the auth config.
	authPayload, err := json.Marshal(authConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal auth config: %w", err)
	}
	log.Debug("auth config JSON marshal is successful")

	// Base64-encode and return.
	return base64.URLEncoding.EncodeToString(authPayload), nil
}

// waitForContainerExit does exactly what the name suggests! It will check,
// every second, the status of the provided container ID, returning either when
// a deadline is hit or the container enters a "done" status, whichever comes
// first.
//
// "Done" here means the container has entered a status of 'Exited' or 'Dead'.
func waitForContainerExit(ctx context.Context, client *client.Client, id string) error {
	log := clog.FromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return context.DeadlineExceeded
		case <-time.After(1 * time.Second):
			// Inspect the container.
			inspect, err := client.ContainerInspect(ctx, id)
			if err != nil {
				log.Error("failed to inspect test container", "error", err)
				continue
			}

			// Evaluate if the container has exited.
			if inspect.State.Status == container.StateDead ||
				inspect.State.Status == container.StateExited {
				return nil
			} else {
				log.Debug(
					"container is still live, waiting longer",
					"state", inspect.State.Status,
				)
			}
		}
	}
}
