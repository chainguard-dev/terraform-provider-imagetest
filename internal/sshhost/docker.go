package sshhost

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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	issh "github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/docker/cli/cli/connhelper"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"github.com/kballard/go-shellquote"
	v1 "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"k8s.io/apimachinery/pkg/util/wait"
)

// OpenSSH ServerAliveInterval/ServerAliveCountMax for the docker dial-stdio
// connection. All docker API calls share one long-lived SSH connection, and a
// silent container (headless browser, long build) can leave it idle for
// minutes, long enough for NAT gateways and idle-connection reapers (~60s) to
// drop it mid-command. Keepalives every 15s keep bytes flowing, the count max
// bounds a dead host to ~2 min. Mirrors the keepalive in internal/ssh.
const (
	sshKeepaliveInterval = 15
	sshKeepaliveCountMax = 8
)

func (h *Host) dockerClient(ctx context.Context) (*client.Client, error) {
	log := clog.FromContext(ctx)

	host := net.JoinHostPort(h.Addr, strconv.Itoa(int(h.Port)))
	url := fmt.Sprintf("ssh://%s", host)

	helper, err := connhelper.GetConnectionHelperWithSSHOpts(url, h.sshOpts())
	if err != nil {
		return nil, fmt.Errorf("creating SSH connection helper: %w", err)
	}

	cli, err := client.New(
		client.WithHTTPClient(&http.Client{
			Transport: &http.Transport{DialContext: helper.Dialer},
		}),
		client.WithHost(url),
		client.WithDialContext(helper.Dialer),
	)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	log.Info("created Docker SSH client", "target", url)
	return cli, nil
}

// sshOpts returns the OpenSSH options for the docker dial-stdio connection.
// With a known_hosts file the host key is verified against it and a mismatch
// aborts the connection; without one, host keys are not checked.
func (h *Host) sshOpts() []string {
	hostKey := []string{"-o", "StrictHostKeyChecking=no"}
	if path := h.KnownHosts; path != "" {
		hostKey = []string{
			"-o", "UserKnownHostsFile=" + path,
			"-o", "StrictHostKeyChecking=yes",
		}
	}
	return append(hostKey,
		"-o", fmt.Sprintf("ServerAliveInterval=%d", sshKeepaliveInterval),
		"-o", fmt.Sprintf("ServerAliveCountMax=%d", sshKeepaliveCountMax),
		"-i", h.KeyPath,
		"-l", h.User,
	)
}

// waitDocker pings the Docker daemon over SSH until it responds. The first
// connection after provisioning is fragile: sshd may be restarting after an
// apt install (needrestart, unattended-upgrades) or the daemon may still be
// coming up, so a single failed ping is not conclusive.
func (h *Host) waitDocker(ctx context.Context, cli *client.Client) error {
	log := clog.FromContext(ctx)
	log.Info("verifying Docker connection")

	backoff := wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   1.0,
		Steps:    24, // 2 min max
	}

	var attempt int
	var lastErr error
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		attempt++
		if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
			lastErr = err
			// OpenSSH refused the host key. Retrying cannot change that, and
			// the real error would otherwise be buried under two minutes of
			// "not reachable yet".
			if strings.Contains(err.Error(), "Host key verification failed") {
				return false, fmt.Errorf("host key verification failed for the docker tunnel (check known_hosts): %w", err)
			}
			log.Warn("Docker not reachable yet, retrying", "attempt", attempt, "error", err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if wait.Interrupted(err) && lastErr != nil {
			err = lastErr
		}
		return fmt.Errorf("docker is not accessible on the host (ensure Docker is installed and running via user_data or setup_commands): %w", err)
	}

	log.Info("Docker connection verified", "attempts", attempt)
	return nil
}

// pullImage pulls ref into the host's Docker daemon, retrying with backoff.
// With PullECRWithHostCredentials set, an image in a private ECR registry is
// pulled on the host with the host's own credentials (see pullOnHost). Every
// other pull sends the caller's registry credentials to the daemon.
func (h *Host) pullImage(ctx context.Context, cli *client.Client, ref name.Reference) error {
	var pull func(context.Context) error
	if h.pullsOnHost(ref) {
		pull = func(ctx context.Context) error { return h.pullOnHost(ctx, ref) }
	} else {
		auth, err := callerRegistryAuth(ref)
		if err != nil {
			return err
		}
		pull = func(ctx context.Context) error { return daemonPull(ctx, cli, ref, auth) }
	}

	var lastErr error
	if err := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    5,
		Cap:      1 * time.Minute,
	}, func(ctx context.Context) (bool, error) {
		if err := pull(ctx); err != nil {
			clog.WarnContext(ctx, "failed to pull image, retrying", "ref", ref.Name(), "error", err)
			lastErr = err
			return false, nil
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("pulling image: %w: last error: %w", err, lastErr)
	}

	return nil
}

// callerRegistryAuth returns the caller's credentials for ref's registry from
// the default keychain, encoded for the Docker API's RegistryAuth.
func callerRegistryAuth(ref name.Reference) (string, error) {
	auth, err := authn.DefaultKeychain.Resolve(ref.Context().Registry)
	if err != nil {
		return "", fmt.Errorf("resolving registry auth: %w", err)
	}

	authConfig, err := auth.Authorization()
	if err != nil {
		return "", fmt.Errorf("getting auth config: %w", err)
	}

	authJSON, err := json.Marshal(authConfig)
	if err != nil {
		return "", fmt.Errorf("marshaling auth config: %w", err)
	}
	return base64.URLEncoding.EncodeToString(authJSON), nil
}

// daemonPull pulls ref through the Docker API with the given RegistryAuth.
func daemonPull(ctx context.Context, cli *client.Client, ref name.Reference, auth string) error {
	result, err := cli.ImagePull(ctx, ref.Name(), client.ImagePullOptions{RegistryAuth: auth})
	if err != nil {
		return err
	}
	defer result.Close()

	_, err = io.Copy(io.Discard, result)
	return err
}

// ecrRegistry matches private ECR registry hosts, such as
// 123456789012.dkr.ecr.us-west-2.amazonaws.com, including FIPS and China
// endpoints. Other registries, ECR Public among them, are pulled with the
// caller's credentials.
var ecrRegistry = regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr(-fips)?\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

// pullsOnHost reports whether ref is pulled on the host with the host's own
// credentials.
func (h *Host) pullsOnHost(ref name.Reference) bool {
	return h.PullECRWithHostCredentials && ecrRegistry.MatchString(ref.Context().RegistryStr())
}

// pullOnHost pulls ref with the docker CLI on the host, which gets ECR
// credentials from docker-credential-ecr-login and the host's own AWS
// credentials. The Docker daemon does not use credential helpers, so the
// pull has to run through the CLI on the host rather than the Docker API.
// Without the helper on the host's PATH, the CLI fails and says so.
func (h *Host) pullOnHost(ctx context.Context, ref name.Reference) error {
	script, err := hostPullScript(ref)
	if err != nil {
		return err
	}

	conn, done, err := h.connect(ctx)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer done()

	stderr := new(bytes.Buffer)
	if err := issh.ExecIn(conn, issh.ShellSh, io.Discard, stderr, script...); err != nil {
		return fmt.Errorf("docker pull on the host: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// hostPullScript returns the sh commands pullOnHost runs. They use a
// throwaway DOCKER_CONFIG that maps only ref's registry to the ECR helper
// ("ecr-login" names docker-credential-ecr-login), so the host user's own
// Docker config is neither read nor changed.
func hostPullScript(ref name.Reference) ([]string, error) {
	config, err := json.Marshal(map[string]any{
		"credHelpers": map[string]string{ref.Context().RegistryStr(): "ecr-login"},
	})
	if err != nil {
		return nil, fmt.Errorf("encoding docker config: %w", err)
	}
	return []string{
		"set -eu",
		`config="$(mktemp -d)"`,
		`trap 'rm -rf "$config"' EXIT`,
		"printf '%s\\n' " + shellquote.Join(string(config)) + ` >"$config/config.json"`,
		`DOCKER_CONFIG="$config" docker pull --quiet ` + shellquote.Join(ref.Name()) + " </dev/null",
	}, nil
}

// PrePullImages pulls images into the host's Docker daemon the same way Run
// pulls the test image (see pullImage): with the caller's keychain, or for
// ECR images with the host's own credentials when PullECRWithHostCredentials
// is set. References are pulled as given (by digest when the caller resolved
// one) and never with a platform override: the daemon selects its own
// platform from a multi-arch index.
func (h *Host) PrePullImages(ctx context.Context, images []string) error {
	if len(images) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)

	refs := make([]name.Reference, 0, len(images))
	for _, s := range images {
		ref, err := name.ParseReference(s)
		if err != nil {
			return fmt.Errorf("parsing pre-pull image %q: %w", s, err)
		}
		refs = append(refs, ref)
	}

	cli, err := h.dockerClient(ctx)
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}
	defer cli.Close()

	if err := h.waitDocker(ctx, cli); err != nil {
		return err
	}

	for _, ref := range refs {
		log.Info("pre-pulling image", "image", ref.String())
		if err := h.pullImage(ctx, cli, ref); err != nil {
			return fmt.Errorf("pre-pulling image %s: %w", ref.String(), err)
		}
	}

	log.Info("pre-pull complete", "count", len(refs))
	return nil
}

func (h *Host) runContainer(ctx context.Context, cli *client.Client, ref name.Reference) (*drivers.RunResult, error) {
	log := clog.FromContext(ctx)

	// The run's label goes in as environment variables so the test can
	// label the containers it starts, see RunEnv. A user Env of the same
	// name is not overridden.
	env := make([]string, 0, len(h.Env)+2)
	for k, v := range h.RunEnv() {
		if _, ok := h.Env[k]; !ok {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range h.Env {
		env = append(env, k+"="+v)
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
		Mounts:        h.mounts(ctx),
		Devices:       h.deviceMappings(ctx),
	}
	hostConfig.DeviceRequests = h.deviceRequests()

	// One name per Run: the container stays until Teardown, and a resource
	// with several tests (or retries) runs them all on this driver.
	containerName := fmt.Sprintf("%s-test-%s", h.Name, uuid.New().String()[:8])
	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        ref.String(),
			User:         "0:0",
			Env:          env,
			Labels:       h.Labels(),
			AttachStdout: true,
			AttachStderr: true,
			Healthcheck: &v1.HealthcheckConfig{
				Test:        append([]string{"CMD"}, entrypoint.DefaultHealthCheckCommand...),
				Interval:    1 * time.Second,
				Timeout:     5 * time.Second,
				Retries:     1,
				StartPeriod: 1 * time.Second,
			},
		},
		HostConfig: hostConfig,
		Name:       containerName,
	})
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}

	log.Info("created container", "id", resp.ID)

	// Register cleanup immediately so container is removed even if we fail below
	if err := h.Stack.Add(func(ctx context.Context) error {
		log.Info("removing container", "id", resp.ID)
		_, err := cli.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{Force: true})
		return err
	}); err != nil {
		return nil, fmt.Errorf("adding container cleanup to stack: %w", err)
	}

	if _, err := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}

	// Create cancellable context for background goroutines
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	waitResult := cli.ContainerWait(runCtx, resp.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	statusCh, errCh := waitResult.Result, waitResult.Error

	logs, err := cli.ContainerLogs(runCtx, resp.ID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching container logs: %w", err)
	}

	var logBuf bytes.Buffer
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
					chunk := string(buf[:n])
					log.Info("container output", "output", chunk)
					logBuf.WriteString(chunk)
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
				inspectResult, err := cli.ContainerInspect(runCtx, resp.ID, client.ContainerInspectOptions{})
				inspect := inspectResult.Container
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

	// Always non-nil so the artifact bundle can be attached even when the test
	// fails — that's exactly when the collected logs matter most.
	result := &drivers.RunResult{}
	var runErr error

	select {
	case err := <-errCh:
		if err != nil {
			runErr = fmt.Errorf("waiting for container: %w", err)
		}
	case status := <-statusCh:
		switch status.StatusCode {
		case 0, int64(entrypoint.ProcessPausedCode):
			// success, or paused via IMAGETEST_SKIP_TEARDOWN
		default:
			runErr = fmt.Errorf("container exited with code %d", status.StatusCode)
		}
	case exitCode := <-healthCh:
		// Health check detected a terminal state
		if exitCode == int64(entrypoint.ProcessPausedCode) {
			log.Info("container paused after success (IMAGETEST_SKIP_TEARDOWN)")
		} else {
			runErr = fmt.Errorf("container health check failed with exit code %d", exitCode)
		}
	case <-ctx.Done():
		runErr = ctx.Err()
	}

	// Cancel background goroutines and wait for log streaming to finish
	cancelRun()
	<-logDone

	// Best-effort: retrieve the artifact bundle the entrypoint wrote (combined
	// process logs plus anything the test dropped in $IMAGETEST_ARTIFACTS). The
	// entrypoint bundles it before exit/pause, so it exists on success, on
	// failure, and while paused. Use a context detached from ctx with its own
	// deadline so the bundle is still retrievable when the run deadline expired.
	artifactCtx, cancelArtifact := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelArtifact()
	if arc, aerr := copyArtifact(artifactCtx, cli, resp.ID, entrypoint.ArtifactsPath); aerr != nil {
		clog.WarnContextf(ctx, "failed to retrieve artifact: %v", aerr)
	} else {
		a, aerr := drivers.NewRunArtifactResult(artifactCtx, arc)
		if cerr := arc.Close(); cerr != nil {
			clog.WarnContextf(ctx, "failed to close artifact reader: %v", cerr)
		}
		if aerr != nil {
			clog.WarnContextf(ctx, "failed to create artifact result: %v", aerr)
		} else {
			result.Artifact = a
		}
	}

	if runErr != nil && logBuf.Len() > 0 {
		runErr = fmt.Errorf("%w\n\nContainer %s logs:\n%s", runErr, containerName, logBuf.String())
	}

	return result, runErr
}

// copyArtifact retrieves a single regular file from a container, unwrapping the
// tar stream the Docker API wraps file copies in. It mirrors
// internal/docker.GetFile, which operates on the wrapper client rather than the
// raw SDK client used here for the SSH transport.
func copyArtifact(ctx context.Context, cli *client.Client, cid, path string) (io.ReadCloser, error) {
	copyResult, err := cli.CopyFromContainer(ctx, cid, client.CopyFromContainerOptions{
		SourcePath: path,
	})
	if err != nil {
		return nil, err
	}
	rc := copyResult.Content

	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		_ = rc.Close()
		if err == io.EOF {
			return nil, fmt.Errorf("no file found in archive for %s", path)
		}
		return nil, err
	}

	return &artifactReader{tr: tr, rc: rc}, nil
}

// artifactReader exposes the current tar entry as a ReadCloser whose Close
// closes the underlying CopyFromContainer stream.
type artifactReader struct {
	tr *tar.Reader
	rc io.ReadCloser
}

func (a *artifactReader) Read(p []byte) (int, error) { return a.tr.Read(p) }
func (a *artifactReader) Close() error               { return a.rc.Close() }

func (h *Host) mounts(ctx context.Context) []mount.Mount {
	if len(h.VolumeMounts) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)
	mounts := make([]mount.Mount, 0, len(h.VolumeMounts))
	for _, vol := range h.VolumeMounts {
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

func (h *Host) deviceMappings(ctx context.Context) []container.DeviceMapping {
	if len(h.DeviceMounts) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)
	devices := make([]container.DeviceMapping, 0, len(h.DeviceMounts))
	for _, dev := range h.DeviceMounts {
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

func (h *Host) deviceRequests() []container.DeviceRequest {
	if h.GPUs == "" {
		return nil
	}

	count := -1 // "all" GPUs
	if h.GPUs != "all" {
		if n, err := strconv.Atoi(h.GPUs); err == nil {
			count = n
		}
	}

	return []container.DeviceRequest{{
		Driver:       "nvidia",
		Count:        count,
		Capabilities: [][]string{{"gpu"}},
	}}
}
