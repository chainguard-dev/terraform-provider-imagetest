package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestRun_Artifacts(t *testing.T) {
	ctx := t.Context()

	const pauseFifoPath = "/tmp/imagetest.unpause"

	tests := []struct {
		name             string
		args             []string
		artifactSetup    map[string]string
		expectedContents map[string]string
		expectedExitCode int
		pauseMode        entrypoint.PauseMode
		expectPause      bool
		wantErr          bool
	}{
		{
			name:             "no artifacts created - empty dir",
			args:             []string{"echo", "success"},
			artifactSetup:    map[string]string{},
			expectedContents: map[string]string{},
			expectedExitCode: 0,
			pauseMode:        entrypoint.PauseNever,
			expectPause:      false,
		},
		{
			name:          "basic artifacts created",
			args:          []string{"/bin/sh", "-c", "mkdir -p $IMAGETEST_ARTIFACTS/logs && echo 'log content' > $IMAGETEST_ARTIFACTS/logs/run.log && echo 'data' > $IMAGETEST_ARTIFACTS/out.txt"},
			artifactSetup: map[string]string{},
			expectedContents: map[string]string{
				"logs":         "__DIR__",
				"logs/run.log": "log content\n",
				"out.txt":      "data\n",
			},
			expectedExitCode: 0,
			pauseMode:        entrypoint.PauseNever,
			expectPause:      false,
		},
		{
			name: "pre-existing artifacts are bundled",
			args: []string{"echo", "process ran"},
			artifactSetup: map[string]string{
				"file1.txt":    "hello",
				"subdir/file2": "world",
			},
			expectedContents: map[string]string{
				"file1.txt":    "hello",
				"subdir":       "__DIR__",
				"subdir/file2": "world",
			},
			expectedExitCode: 0,
			pauseMode:        entrypoint.PauseNever,
			expectPause:      false,
		},
		{
			name: "artifacts bundled even if process fails",
			args: []string{"/bin/sh", "-c", "echo 'partial data' > $IMAGETEST_ARTIFACTS/partial.log; exit 1"},
			artifactSetup: map[string]string{
				"previous.txt": "old data",
			},
			expectedContents: map[string]string{
				"previous.txt": "old data",
				"partial.log":  "partial data\n",
			},
			expectedExitCode: 1,
			pauseMode:        entrypoint.PauseNever,
			expectPause:      false,
		},
		{
			name:          "artifacts bundled when pausing on error",
			args:          []string{"/bin/sh", "-c", "echo 'error artifact' > $IMAGETEST_ARTIFACTS/error.txt; exit 3"},
			artifactSetup: map[string]string{},
			expectedContents: map[string]string{
				"error.txt": "error artifact\n",
			},
			expectedExitCode: 3,
			pauseMode:        entrypoint.PauseOnError,
			expectPause:      true,
		},
		{
			name: "artifacts bundled when pausing always",
			args: []string{"echo", "success artifact"},
			artifactSetup: map[string]string{
				"always.txt": "always bundled",
			},
			expectedContents: map[string]string{
				"always.txt": "always bundled",
			},
			expectedExitCode: entrypoint.ProcessPausedCode,
			pauseMode:        entrypoint.PauseAlways,
			expectPause:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			artifactsDir := filepath.Join(tmpDir, "artifacts")
			artifactPath := filepath.Join(tmpDir, "bundle.tar.gz")
			logPath := filepath.Join(tmpDir, "process.log")

			if err := os.Remove(pauseFifoPath); err != nil {
				t.Logf("Failed to remove pause FIFO: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Remove(pauseFifoPath); err != nil {
					t.Logf("Failed to remove pause FIFO: %v", err)
				}
			})

			if tt.pauseMode != "" {
				t.Setenv(entrypoint.PauseModeEnvVar, string(tt.pauseMode))
			} else {
				if err := os.Unsetenv(entrypoint.PauseModeEnvVar); err != nil {
					t.Logf("Failed to unset pause mode: %v", err)
				}
			}

			t.Setenv(entrypoint.AritfactsDirEnvVar, artifactsDir)

			if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
				t.Fatalf("Failed to create artifacts directory: %v", err)
			}
			setupTestArtifacts(t, artifactsDir, tt.artifactSetup)

			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			opts := &opts{
				healthStatus:   newHealthStatus(),
				args:           tt.args,
				ProcessLogPath: logPath,
				ArtifactsDir:   artifactsDir,
				ArtifactPath:   artifactPath,
				WaitForProbe:   false,
				CommandTimeout: 5 * time.Second,
				GracePeriod:    1 * time.Second,
			}
			switch mode := os.Getenv(entrypoint.PauseModeEnvVar); mode {
			case string(entrypoint.PauseAlways):
				opts.PauseMode = entrypoint.PauseAlways
			case string(entrypoint.PauseOnError):
				opts.PauseMode = entrypoint.PauseOnError
			default:
				opts.PauseMode = entrypoint.PauseNever
			}

			defer os.Remove(entrypoint.DefaultHealthCheckSocket)

			done := make(chan struct{})
			if tt.expectPause {
				go func() {
					defer close(done)
					for i := 0; i < 10; i++ {
						if _, err := os.Stat(pauseFifoPath); err == nil {
							break
						}
						time.Sleep(100 * time.Millisecond)
					}

					if _, err := os.Stat(pauseFifoPath); err != nil {
						t.Logf("FIFO not found after wait: %v", err)
						return
					}

					f, err := os.OpenFile(pauseFifoPath, os.O_WRONLY, 0)
					if err != nil {
						t.Logf("Failed to open FIFO for writing: %v", err)
						return
					}
					defer f.Close()

					_, err = f.Write([]byte{1})
					if err != nil {
						t.Logf("Failed to write to FIFO: %v", err)
					}
				}()
			} else {
				close(done)
			}

			exitCode := opts.Run(ctx)

			<-done

			if exitCode != tt.expectedExitCode {
				t.Errorf("Unexpected final exit code from Run(): got %d, want %d", exitCode, tt.expectedExitCode)
			}

			verifyTarballContents(t, artifactPath, tt.expectedContents)
		})
	}
}

func verifyTarballContents(t *testing.T, tarballPath string, expected map[string]string) {
	t.Helper()

	f, err := os.Open(tarballPath)
	if err != nil {
		t.Fatalf("Failed to open tarball %s: %v", tarballPath, err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("Failed to create gzip reader for %s: %v", tarballPath, err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	actual := make(map[string]string)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Error reading tar header from %s: %v", tarballPath, err)
		}

		normalizedName := filepath.ToSlash(hdr.Name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			actual[normalizedName] = "__DIR__"
		case tar.TypeSymlink:
			actual[normalizedName] = fmt.Sprintf("__SYMLINK__:%s", filepath.ToSlash(hdr.Linkname))
		case tar.TypeReg:
			content, err := io.ReadAll(io.LimitReader(tr, 1*1024*1024))
			if err != nil {
				t.Fatalf("Error reading file content from tar for %s in %s: %v", normalizedName, tarballPath, err)
			}
			actual[normalizedName] = string(content)
		default:
			t.Logf("Skipping unexpected tar entry type in %s: %s (type: %v)", tarballPath, normalizedName, hdr.Typeflag)
		}
	}

	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Errorf("Tarball contents mismatch for %s (-want +got):\n%s", tarballPath, diff)
	}
}

func setupTestArtifacts(t *testing.T, dir string, structure map[string]string) {
	t.Helper()
	for path, content := range structure {
		fullPath := filepath.Join(dir, path)
		if content == "__DIR__" {
			if err := os.MkdirAll(fullPath, 0o755); err != nil {
				t.Fatalf("failed to create directory %s: %v", fullPath, err)
			}
		} else if strings.HasPrefix(content, "__SYMLINK__:") {
			target := strings.TrimPrefix(content, "__SYMLINK__:")
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				t.Fatalf("failed to create parent directory for symlink %s: %v", fullPath, err)
			}
			if err := os.Symlink(target, fullPath); err != nil {
				t.Fatalf("failed to create symlink %s -> %s: %v", fullPath, target, err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				t.Fatalf("failed to create parent directory for file %s: %v", fullPath, err)
			}
			if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
				t.Fatalf("failed to write file %s: %v", fullPath, err)
			}
		}
	}
}
