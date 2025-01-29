// entrypoint is **heavily** inspired by prow's entrypoint package:
// https://github.com/kubernetes-sigs/prow/tree/main/pkg/entrypoint
//
// The major differences are the absence of marker files, and a refreshed
// context/clog implementation. As imagetest doesn't use prow's markers, we
// strictly rely on the exit codes and containers that run to completion.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

	flag.Parse()

	opts.args = flag.Args()

	return opts
}

func main() {
	opts := parseFlags()

	log := clog.New(slog.Default().Handler())
	ctx := clog.WithLogger(context.Background(), log)

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		// Run the binary as a health check
		conn, err := net.Dial("unix", entrypoint.DefaultHealthCheckSocket)
		if err != nil {
			clog.ErrorContextf(ctx, "failed to connect to health socket: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}
		defer conn.Close()

		var status healthStatus
		if err := json.NewDecoder(conn).Decode(&status); err != nil {
			clog.ErrorContextf(ctx, "failed to decode health status: %v", err)
			os.Exit(entrypoint.InternalErrorCode)
		}

		switch status.State {
		case healthRunning:
			clog.InfoContext(ctx, status.Message)
			os.Exit(0)
		case healthPaused:
			clog.InfoContext(ctx, status.Message)
			os.Exit(entrypoint.ProcessPausedErrorCode)
		case healthFailed:
			clog.InfoContext(ctx, status.Message)
			os.Exit(entrypoint.InternalErrorCode)
		}

		os.Exit(0)
	}

	// Run the binary as an entrypoint
	code := opts.Run(ctx)
	os.Exit(code)
}

func (o *opts) Run(ctx context.Context) int {
	code, err := o.executeProcess(ctx)
	if err != nil {
		clog.ErrorContextf(ctx, "Error executing wrapped process: %v", err)
		o.healthStatus.update(healthFailed, err.Error())

		return code
	}

	return code
}

func (o *opts) executeProcess(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, o.CommandTimeout)
	defer cancel()

	healthCleanup, err := o.healthStatus.startSocket()
	if err != nil {
		return entrypoint.InternalErrorCode, fmt.Errorf("failed to start health socket: %w", err)
	}
	defer healthCleanup()

	stdoutw := io.Writer(os.Stdout)
	stderrw := io.Writer(os.Stderr)

	if o.ProcessLogPath != "" {
		if err := os.MkdirAll(filepath.Dir(o.ProcessLogPath), 0755); err != nil {
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

	ef, err := os.Create(entrypoint.DefaultStderrLogPath)
	if err != nil {
		clog.ErrorContextf(ctx, "Error writing stderr log: %v", err)
	}
	defer ef.Close()
	stderrw = io.MultiWriter(stderrw, ef)

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
			// grab stderr from the file
			errdata, err := os.ReadFile(entrypoint.DefaultStderrLogPath)
			if err != nil {
				clog.ErrorContextf(ctx, "Error reading stderr log: %v", err)
			}

			errmsg := fmt.Sprintf("%s (exit code %d)",
				errdata,
				exitErr.ExitCode(),
			)

			if os.Getenv("IMAGETEST_PAUSE_ON_ERROR") != "" {
				o.healthStatus.update(healthPaused, errmsg)
				if err := pause(ctx, exitErr.ExitCode()); err != nil {
					return entrypoint.InternalErrorCode, err
				}

				// after a successful pause, just exit with the originally captured exit code
				return exitErr.ExitCode(), nil
			}

			// wrapped process exiting with an error, just surface it
			return exitErr.ExitCode(), fmt.Errorf("%s", errmsg)
		}

		// we got an error while force killing
		return entrypoint.InternalErrorCode, waitErr
	}

	return 0, nil
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

func pause(parentCtx context.Context, exitCode int) error {
	fifoPath := "/tmp/imagetest.unpause"
	clog.ErrorContext(parentCtx, "wrapped process failed, attempting to pause for debugging", "fifo_path", fifoPath, "exit_code", exitCode)

	// create a new context to avoid exiting early if the parent context is cancelled
	pauseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pauseCtx, stop := signal.NotifyContext(pauseCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := os.Remove(fifoPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove debug fifo: %w", err)
	}

	if err := syscall.Mkfifo(fifoPath, 0622); err != nil {
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

type healthState string

const (
	healthStarting healthState = "starting"
	healthRunning  healthState = "running"
	healthPaused   healthState = "paused"
	healthFailed   healthState = "failed"
)

type healthStatus struct {
	State   healthState `json:"state"`
	Time    time.Time   `json:"time"`
	Message string      `json:"message"`
	mu      sync.RWMutex

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

func (h *healthStatus) update(state healthState, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.State = state
	h.Message = message
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
		listener.Close()
		os.Remove(entrypoint.DefaultHealthCheckSocket)
	}, nil
}

func (h *healthStatus) markProbed() {
	h.probedOnce.Do(func() {
		h.update(healthRunning, "marking as probed")
		close(h.probed)
	})
}
