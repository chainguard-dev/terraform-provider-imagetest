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

// testsHandler is an internal slog handler that only writes driver_log attribute values to a file.
type testsHandler struct {
	w io.WriteCloser
}

func (d *testsHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (d *testsHandler) Handle(_ context.Context, record slog.Record) error {
	// Look for the driver_log attribute
	var driverLog string
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == drivers.LogAttributeKey {
			driverLog = a.Value.String()
			return false // stop iteration
		}
		return true
	})

	// Only write if we found a driver_log attribute
	if driverLog != "" {
		_, err := fmt.Fprintln(d.w, driverLog)
		return err
	}

	return nil
}

func (d *testsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Return the same handler - we don't need to track attrs
	return d
}

func (d *testsHandler) WithGroup(name string) slog.Handler {
	// Return the same handler - we don't need to track groups
	return d
}
