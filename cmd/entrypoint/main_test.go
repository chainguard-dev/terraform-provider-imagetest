package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/google/go-cmp/cmp"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		expectedCode  int
		expectedLog   string
		expectedError string
		overrideOpts  func(*opts)
	}{
		{
			name:         "successful command",
			args:         []string{"echo", "hello world"},
			expectedCode: 0,
			overrideOpts: func(o *opts) {
				o.WaitForProbe = false
			},
		},
		{
			name:         "failed process uses process exit code",
			args:         []string{"/bin/sh", "-c", "exit 42"},
			expectedCode: 42,
			overrideOpts: func(o *opts) {
				o.WaitForProbe = false
			},
		},
		{
			name: "command times out",
			args: []string{"sleep", "10"},
			overrideOpts: func(o *opts) {
				o.WaitForProbe = false
				o.CommandTimeout = 1 * time.Second
				o.GracePeriod = 1 * time.Second
			},
			expectedCode:  entrypoint.InternalErrorCode,
			expectedError: "process timed out or cancelled",
		},
		{
			name: "command forks logs to file",
			args: []string{"echo", "hello world"},
			overrideOpts: func(o *opts) {
				o.WaitForProbe = false
			},
			expectedLog: "hello world\n",
		},
		{
			name: "internal failure uses internal error code",
			args: []string{"invalid"},
			overrideOpts: func(o *opts) {
				o.WaitForProbe = false
			},
			expectedCode: entrypoint.InternalErrorCode,
		},
		{
			name: "process blocks until probed",
			args: []string{"echo", "hello world"},
			overrideOpts: func(o *opts) {
				o.GracePeriod = 1 * time.Second
			},
			expectedCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original args
			oldArgs := os.Args
			defer func() {
				os.Args = oldArgs
				flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			}()

			// Set up new args for this test
			os.Args = append([]string{"cmd"}, tt.args...)

			opts := parseFlags()
			opts.args = tt.args
			opts.ProcessLogPath = filepath.Join(t.TempDir(), "process.log")

			if tt.overrideOpts != nil {
				tt.overrideOpts(opts)
			}

			if opts.WaitForProbe {
				go func() {
					// janky way to probe
					time.Sleep(3 * time.Second)
					opts.healthStatus.markProbed()
				}()
			}

			code, err := opts.executeProcess(t.Context())

			// Validate the result
			if err != nil && tt.expectedCode == 0 {
				t.Fatalf("unexpected error: %v", err)
			}

			if code != tt.expectedCode {
				t.Errorf("expected code %d, got %d", tt.expectedCode, code)
			}

			if err != nil && tt.expectedError != "" {
				if diff := cmp.Diff(tt.expectedError, err.Error()); diff != "" {
					t.Errorf("unexpected error (-want +got):\n%s", diff)
				}
			}

			if tt.expectedLog != "" {
				content, err := os.ReadFile(opts.ProcessLogPath)
				if err != nil {
					t.Fatalf("failed to read process log: %v", err)
				}

				if diff := cmp.Diff(tt.expectedLog, string(content)); diff != "" {
					t.Errorf("unexpected log content (-want +got):\n%s", diff)
				}
			}
		})
	}
}
