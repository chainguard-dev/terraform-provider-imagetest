package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/gosimple/slug"
	slogmulti "github.com/samber/slog-multi"
)

// SetupTestsLogging configures logging with optional file output for a specific test.
func SetupTestsLogging(ctx context.Context, logsDirectory, testID, testName string) (context.Context, func()) {
	if logsDirectory == "" {
		return ctx, func() {}
	}

	// Create subdirectory for this test resource
	testDir := filepath.Join(logsDirectory, testID)
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		clog.WarnContext(ctx, "failed to create test directory", "path", testDir, "error", err.Error())
		return ctx, func() {}
	}

	// Create a safe filename for this test
	safeTestName := slug.Make(testName)
	logPath := filepath.Join(testDir, fmt.Sprintf("%s.log", safeTestName))

	logFile, err := os.Create(logPath)
	if err != nil {
		clog.WarnContext(ctx, "failed to create test log file", "path", logPath, "error", err.Error())
		return ctx, func() {}
	}

	// Create a custom handler that only writes driver_log content
	fileHandler := &testsHandler{
		w: logFile,
	}

	// Use slog-multi to tee to both handlers
	handler := clog.FromContext(ctx).Handler()
	handler = slogmulti.Fanout(handler, fileHandler)

	// Update the context with the new handler
	clog.InfoContext(ctx, "logging test output to file", "path", logPath)
	ctx = clog.WithLogger(ctx, clog.New(handler))

	return ctx, func() {
		if err := logFile.Close(); err != nil {
			clog.WarnContext(ctx, "failed to close log file", "path", logPath, "error", err.Error())
		}
	}
}

// testsHandler is an internal slog handler that only writes driver log output to a file.
// It tracks whether driver_log was set via WithAttrs (context-level), avoiding
// per-record attribute iteration.
type testsHandler struct {
	w           io.WriteCloser
	isDriverLog bool
}

func (d *testsHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return d.isDriverLog
}

func (d *testsHandler) Handle(_ context.Context, record slog.Record) error {
	_, err := fmt.Fprintln(d.w, record.Message)
	return err
}

func (d *testsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	for _, a := range attrs {
		if a.Key == drivers.LogAttributeKey {
			return &testsHandler{w: d.w, isDriverLog: true}
		}
	}
	return d
}

func (d *testsHandler) WithGroup(_ string) slog.Handler {
	return d
}
