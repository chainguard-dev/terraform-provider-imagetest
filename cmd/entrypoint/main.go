// entrypoint is **heavily** inspired by prow's entrypoint package:
// https://github.com/kubernetes-sigs/prow/tree/main/pkg/entrypoint
//
// The major differences are the absence of marker files, and a refreshed
// context/clog implementation. As imagetest doesn't use prow's markers, we
// strictly rely on the exit codes and containers that run to completion.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
)

const (
	DefaultTimeout = 60 * time.Minute
	GracePeriod    = 15 * time.Second
)

type opts struct {
	ProcessLogPath string
	CommandTimeout time.Duration
	GracePeriod    time.Duration
	WaitForProbe   bool
	PauseMode      entrypoint.PauseMode
	ArtifactsDir   string
	ArtifactPath   string

	healthStatus *healthStatus
	args         []string
}

func parseFlags() *opts {
	opts := &opts{
		healthStatus: newHealthStatus(),
	}

	flag.StringVar(&opts.ProcessLogPath, "process-log-path", "", "Path to the log file for the process")
	flag.DurationVar(&opts.CommandTimeout, "timeout", DefaultTimeout, "How long to allow the process to run before cancelling it")
	flag.DurationVar(&opts.GracePeriod, "grace-period", GracePeriod, "How long to wait for the process to exit gracefully after sending a SIGINT before sending a SIGKILL")
	flag.BoolVar(&opts.WaitForProbe, "wait-for-probe", true, "Wait for the entrypoint to be probed before starting the wrapped process")
	flag.StringVar(&opts.ArtifactsDir, "artifacts-dir", entrypoint.ArtifactsDir, "Path to the directory where artifacts should be stored")
	flag.StringVar(&opts.ArtifactPath, "artifact-path", entrypoint.ArtifactsPath, "Path to the packaged artifact tarball")

	flag.Parse()

	opts.args = flag.Args()

	switch mode := os.Getenv(entrypoint.PauseModeEnvVar); mode {
	case string(entrypoint.PauseAlways):
		opts.PauseMode = entrypoint.PauseAlways
	case string(entrypoint.PauseOnError):
		opts.PauseMode = entrypoint.PauseOnError
	}

	return opts
}

func main() {
	opts := parseFlags()

	log := clog.New(slog.Default().Handler())
	ctx := clog.WithLogger(context.Background(), log)

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Run the entrypoint as a health check
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		logger := clog.New(slog.NewJSONHandler(os.Stdout, nil))

		conn, err := net.Dial("unix", entrypoint.DefaultHealthCheckSocket)
		if err != nil {
			logger.ErrorContextf(ctx, "failed to connect to health socket: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}
		defer conn.Close()

		var status healthStatus
		if err := json.NewDecoder(conn).Decode(&status); err != nil {
			logger.ErrorContextf(ctx, "failed to decode health status: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}

		logger.InfoContext(ctx, status.Message, "exit_code", status.ExitCode)

		switch status.State {
		case healthRunning:
			os.Exit(0)
		case healthPausedWithError:
			os.Exit(entrypoint.ProcessPausedWithErrorCode)
		case healthPaused:
			os.Exit(entrypoint.ProcessPausedCode)
		case healthFailed:
			os.Exit(entrypoint.InternalErrorCode)
		}

		os.Exit(0)
	}

	// Run the entrypoint and block via the pause
	if len(os.Args) == 2 && os.Args[1] == "wait" {
		if err := wait(ctx); err != nil {
			clog.ErrorContextf(ctx, "failed to wait: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}

		os.Exit(0)
	}

	// Bundle the artifact dir and export it to stdout
	if len(os.Args) == 2 && os.Args[1] == "export" {
		f, err := os.Open(opts.ArtifactPath)
		if err != nil {
			clog.ErrorContextf(ctx, "failed to open artifact path: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}
		defer f.Close()

		if _, err := io.Copy(os.Stdout, f); err != nil {
			clog.ErrorContextf(ctx, "failed to copy artifact to stdout: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}

		// after the copy is done, send a resume signal
		if err := resume(); err != nil {
			clog.ErrorContextf(ctx, "failed to resume: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}

		os.Exit(0)
	}

	// Maybe start a registry proxy
	if os.Getenv(entrypoint.DriverLocalRegistryEnvVar) != "" {
		port, err := strconv.Atoi(os.Getenv(entrypoint.DriverLocalRegistryPortEnvVar))
		if err != nil {
			clog.ErrorContextf(ctx, "failed to parse registry port: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}

		ps := proxyServer{
			port:    port,
			logPath: filepath.Join("/tmp", "registry-proxy.log"),
		}

		// We don't really care about shutdowns here
		go func() {
			clog.InfoContext(ctx, "Starting proxy server")
			if err := ps.Start(); err != nil {
				clog.ErrorContextf(ctx, "failed to start server: %v", err)
			}
		}()
	}

	// Run the binary as an entrypoint
	code := opts.Run(ctx)
	clog.InfoContextf(ctx, "exiting with code: %d", code)
	os.Exit(code)
}

func (o *opts) Run(ctx context.Context) int {
	healthCleanup, err := o.healthStatus.startSocket()
	if err != nil {
		clog.ErrorContextf(ctx, "failed to start health socket: %v", err)
		return entrypoint.InternalErrorCode
	}
	defer healthCleanup()

	code, err := o.executeProcess(ctx)
	if err != nil {
		clog.ErrorContextf(ctx, "wrapped process exited with exit code %d", code)
		o.healthStatus.update(healthFailed, err.Error(), int64(code))
	}

	return o.finalize(ctx, code, err)
}

func (o *opts) executeProcess(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, o.CommandTimeout)
	defer cancel()

	stdoutw := io.Writer(os.Stdout)
	stderrw := io.Writer(os.Stderr)

	if o.ProcessLogPath != "" {
		if err := os.MkdirAll(filepath.Dir(o.ProcessLogPath), 0o755); err != nil {
			return entrypoint.InternalErrorCode, fmt.Errorf("failed to create process log directory: %w", err)
		}

		plf, err := os.Create(o.ProcessLogPath)
		if err != nil {
			return entrypoint.InternalErrorCode, fmt.Errorf("failed to create process log file: %w", err)
		}
		defer plf.Close()

		// Write both stdout and stderr to the process log
		stdoutw = io.MultiWriter(stdoutw, plf)
		stderrw = io.MultiWriter(stderrw, plf)
	}

	if len(o.args) == 0 {
		return entrypoint.InternalErrorCode, fmt.Errorf("no command provided")
	}
	cmdName, cmdArgs := o.args[0], o.args[1:]

	cmd := exec.Command(cmdName, cmdArgs...)
	cmd.Stdout = stdoutw
	cmd.Stderr = stderrw
	cmd.Env = append(os.Environ(), "IMAGETEST=true")

	// Block until we are probed
	if o.WaitForProbe {
		clog.InfoContext(ctx, "waiting for probe before starting wrapped process")

		select {
		case <-ctx.Done():
			clog.InfoContext(ctx, "context cancelled before probe received")
			return entrypoint.InternalErrorCode, ctx.Err()
		case <-o.healthStatus.probed:
			clog.InfoContext(ctx, "probed, starting wrapped process")
		}
	}

	clog.InfoContext(ctx, "starting wrapped process", "cmd", cmdName, "args", cmdArgs)
	if err := cmd.Start(); err != nil {
		return entrypoint.InternalErrorCode, fmt.Errorf("failed to start the process: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-done:
		// Child finished before timeout
	case <-ctx.Done():
		// Process timed out or cancelled
		clog.InfoContext(ctx, "process timed out")
		waitErr = errors.New("process timed out or cancelled")
		gracefullyTerminate(ctx, cmd, o.GracePeriod)

		<-done
	}

	// extract the exit code from the error
	if waitErr != nil {
		var exitErr *exec.ExitError

		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode(), exitErr
		}

		// we got an error while force killing
		return entrypoint.InternalErrorCode, waitErr
	}

	return 0, nil
}

func (o *opts) finalize(ctx context.Context, code int, execErr error) int {
	berr := o.bundleArtifacts(ctx)
	if berr != nil {
		clog.ErrorContextf(ctx, "failed to bundle artifacts: %v", berr)
		// Let this fallthrough so we don't block the pause, but depending on the pause we may surface berr
	}

	// TODO: refactor to QF1001: could apply De Morgan's law (staticcheck)
	if o.PauseMode != entrypoint.PauseAlways &&
		!(execErr != nil && o.PauseMode == entrypoint.PauseOnError) { //nolint: staticcheck

		if berr != nil {
			return entrypoint.InternalErrorCode
		}
		return code
	}

	// we're pausing one way or another

	if execErr != nil && (o.PauseMode == entrypoint.PauseOnError || o.PauseMode == entrypoint.PauseAlways) {
		// we're pausing with an error
		o.healthStatus.update(healthPausedWithError, "pausing after error observed", int64(entrypoint.ProcessPausedWithErrorCode))
		if err := pause(ctx, code); err != nil {
			clog.ErrorContextf(ctx, "failed to pause: %v", err)
			return entrypoint.InternalErrorCode
		}

		return code
	}

	o.healthStatus.update(healthPaused, "pausing after successful execution", int64(entrypoint.ProcessPausedCode))
	if err := pause(ctx, code); err != nil {
		clog.ErrorContextf(ctx, "failed to pause: %v", err)
		return entrypoint.InternalErrorCode
	}

	return entrypoint.ProcessPausedCode
}

// bundleArtifacts builds the artifacts bundle suitable for exfiltration/upload.
func (o *opts) bundleArtifacts(ctx context.Context) error {
	if err := os.MkdirAll(o.ArtifactsDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory for artifact file %s: %w", o.ArtifactPath, err)
	}

	af, err := os.Create(o.ArtifactPath)
	if err != nil {
		return err
	}

	h := sha256.New()

	mw := io.MultiWriter(af, h)

	gzw := gzip.NewWriter(mw)
	tw := tar.NewWriter(gzw)

	err = filepath.WalkDir(o.ArtifactsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("error accessing path %s during walk: %w", path, walkErr)
		}

		if path == o.ArtifactsDir {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to get FileInfo for %s: %w", path, err)
		}

		// Don't chase symlinks
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header for %s: %w", path, err)
		}

		rpath, err := filepath.Rel(o.ArtifactsDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}
		hdr.Name = filepath.ToSlash(rpath)

		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("failed to write tar header for %s: %w", path, err)
		}

		if !fi.IsDir() && fi.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s for tarring: %w", path, err)
			}
			defer f.Close()

			if _, err := io.CopyN(tw, f, fi.Size()); err != nil {
				return fmt.Errorf("failed to copy file content %s to tar archive: %w", path, err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed walking artifacts directory %s: %w", o.ArtifactsDir, err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}

	if err := gzw.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	if err := af.Close(); err != nil {
		return fmt.Errorf("failed to close artifact file: %w", err)
	}

	fi, err := os.Stat(o.ArtifactPath)
	if err != nil {
		return fmt.Errorf("failed to get file info for artifact file: %w", err)
	}

	clog.InfoContext(ctx, "finished bundling artifacts",
		"target", o.ArtifactPath,
		"dir", o.ArtifactsDir,
		"size", fi.Size(),
		"hash", fmt.Sprintf("%x", h.Sum(nil)),
	)
	return nil
}

// gracefullyTerminate sends a SIGINT, waits for gracePeriod, then sends a SIGKILL.
func gracefullyTerminate(ctx context.Context, cmd *exec.Cmd, gracePeriod time.Duration) {
	if cmd.Process == nil {
		return
	}

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		clog.ErrorContextf(ctx, "failed to send SIGINT to process: %v", err)
		return
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		clog.InfoContext(ctx, "process exited gracefully after SIGINT")
		return
	case <-time.After(gracePeriod):
		clog.InfoContext(ctx, "process did not exit gracefully after SIGINT, sending SIGKILL")
		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
			clog.ErrorContextf(ctx, "failed to send SIGKILL to process: %v", err)
			return
		}
	}
}

// resume writes some bytes to the named pipe to resume the process.
func resume() error {
	if err := os.WriteFile("/tmp/imagetest.unpause", []byte("resume"), 0o644); err != nil {
		return fmt.Errorf("failed to write to resume file: %w", err)
	}
	return nil
}

func pause(parentCtx context.Context, exitCode int) error {
	fifoPath := "/tmp/imagetest.unpause"
	clog.InfoContext(parentCtx, "attempting to pause for debugging", "fifo_path", fifoPath, "exit_code", exitCode)

	// create a new context to avoid exiting early if the parent context is cancelled
	pauseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pauseCtx, stop := signal.NotifyContext(pauseCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := os.Remove(fifoPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove debug fifo: %w", err)
	}

	if err := syscall.Mkfifo(fifoPath, 0o622); err != nil {
		return fmt.Errorf("failed to create debug fifo: %w", err)
	}
	defer os.Remove(fifoPath)

	unpaused := make(chan struct{})
	errChan := make(chan error, 1)

	go func() {
		// open the FIFO in blocking mode
		fd, err := syscall.Open(fifoPath, syscall.O_RDONLY, 0)
		if err != nil {
			errChan <- fmt.Errorf("failed to open FIFO: %w", err)
			return
		}
		defer syscall.Close(fd)

		// block until we read a single byte from somewhere (presumably the user)
		buf := make([]byte, 1)
		_, err = syscall.Read(fd, buf)
		if err != nil {
			errChan <- fmt.Errorf("failed to read from FIFO: %w", err)
			return
		}

		close(unpaused)
	}()

	clog.InfoContextf(parentCtx, "successfully paused, to resume, run: echo > %s", fifoPath)

	for {
		select {
		case <-pauseCtx.Done():
			return fmt.Errorf("debugging interrupted: %w", pauseCtx.Err())
		case err := <-errChan:
			return fmt.Errorf("FIFO error: %w", err)
		case <-unpaused:
			clog.InfoContext(parentCtx, "resuming execution")
			return nil
		}
	}
}

// wait handles the "wait" command, and simply blocks until the process is resumed with a health check server so that it responds to probes.
func wait(ctx context.Context) error {
	clog.InfoContext(ctx, "starting wait...")

	health := newHealthStatus()
	teardown, err := health.startSocket()
	if err != nil {
		return fmt.Errorf("failed to start health socket: %w", err)
	}
	defer teardown()

	health.update(healthPaused, "paused in wait mode", 0)

	if err := pause(ctx, 0); err != nil {
		return fmt.Errorf("failed to pause: %w", err)
	}

	clog.InfoContext(ctx, "finished pausing")
	return nil
}

type healthState string

const (
	healthStarting        healthState = "starting"
	healthRunning         healthState = "running"
	healthPaused          healthState = "paused"
	healthPausedWithError healthState = "paused_with_error"
	healthFailed          healthState = "failed"
)

type healthStatus struct {
	State    healthState `json:"state"`
	Time     time.Time   `json:"time"`
	Message  string      `json:"message"`
	ExitCode int64       `json:"exit_code"`
	mu       sync.RWMutex

	probed     chan struct{}
	probedOnce sync.Once
}

func newHealthStatus() *healthStatus {
	return &healthStatus{
		State:      healthStarting,
		Time:       time.Now(),
		Message:    "starting",
		mu:         sync.RWMutex{},
		probed:     make(chan struct{}),
		probedOnce: sync.Once{},
	}
}

func (h *healthStatus) update(state healthState, message string, exitCode int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.State = state
	h.Message = message
	h.ExitCode = exitCode
	// This ends up being an approximation, but we don't need to be super precise
	h.Time = time.Now()
}

func (h *healthStatus) startSocket() (func(), error) {
	if err := os.Remove(entrypoint.DefaultHealthCheckSocket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to remove health socket: %w", err)
	}

	listener, err := net.Listen("unix", entrypoint.DefaultHealthCheckSocket)
	if err != nil {
		return nil, fmt.Errorf("failed to create health socket: %w", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()

			// Take out a lock, since this is on the same goroutine it blocks, but we
			// only ever expect this to be called by runtimes during health checks
			h.mu.RLock()
			if err := json.NewEncoder(conn).Encode(h); err != nil {
				return
			}
			h.mu.RUnlock()

			h.markProbed()
		}
	}()

	return func() {
		_ = listener.Close()
		_ = os.Remove(entrypoint.DefaultHealthCheckSocket)
	}, nil
}

func (h *healthStatus) markProbed() {
	h.probedOnce.Do(func() {
		h.update(healthRunning, "marking as probed", 0)
		close(h.probed)
	})
}

type proxyServer struct {
	port    int
	logPath string
}

func (p *proxyServer) Start() error {
	lf, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(lf, nil))

	proxy := httputil.NewSingleHostReverseProxy(&url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("host.docker.internal:%d", p.port),
	})

	// Wrap the director with a simple logger
	odirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		odirector(req)
		logger.Info("request",
			"method", req.Method,
			"url", req.URL.String(),
			"host", req.Host,
			"remote", req.RemoteAddr)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		logger.Info("response",
			"method", resp.Request.Method,
			"url", resp.Request.URL.String(),
			"status", resp.StatusCode,
			"size", resp.ContentLength)
		return nil
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", p.port),
		Handler: proxy,
	}

	return server.ListenAndServe()
}
