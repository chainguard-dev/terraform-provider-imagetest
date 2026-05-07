package dockeronhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDockerShim runs the shim through /bin/sh against a stubbed "real
// docker" that just echoes its argv, so we can directly assert the
// resulting command line for various input shapes.
func TestDockerShim(t *testing.T) {
	tmp := t.TempDir()

	stubReal := filepath.Join(tmp, "real-docker")
	if err := os.WriteFile(stubReal, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Point the shim's REAL at our stub instead of /usr/bin/docker.
	shimSrc := strings.Replace(dockerShim, "REAL=/usr/bin/docker", "REAL="+stubReal, 1)
	if !strings.Contains(shimSrc, "REAL="+stubReal) {
		t.Fatal("failed to redirect REAL to stub")
	}
	shimFile := filepath.Join(tmp, "docker")
	if err := os.WriteFile(shimFile, []byte(shimSrc), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		label string
		args  []string
		want  string
	}{
		{
			name:  "empty_label_passes_through",
			label: "",
			args:  []string{"run", "image"},
			want:  "run image",
		},
		{
			name:  "no_args_passes_through",
			label: "imagetest.test=foo",
			args:  []string{},
			want:  "",
		},
		{
			name:  "run_injects_label_first",
			label: "imagetest.test=foo",
			args:  []string{"run", "image"},
			want:  "run --label imagetest.test=foo image",
		},
		{
			name:  "run_preserves_subsequent_flags",
			label: "imagetest.test=foo",
			args:  []string{"run", "--rm", "-d", "image", "cmd"},
			want:  "run --label imagetest.test=foo --rm -d image cmd",
		},
		{
			name:  "create_injects_label",
			label: "imagetest.test=foo",
			args:  []string{"create", "image"},
			want:  "create --label imagetest.test=foo image",
		},
		{
			name:  "network_create_injects_label",
			label: "imagetest.test=foo",
			args:  []string{"network", "create", "mynet"},
			want:  "network create --label imagetest.test=foo mynet",
		},
		{
			name:  "volume_create_injects_label",
			label: "imagetest.test=foo",
			args:  []string{"volume", "create", "myvol"},
			want:  "volume create --label imagetest.test=foo myvol",
		},
		{
			name:  "network_ls_passes_through",
			label: "imagetest.test=foo",
			args:  []string{"network", "ls"},
			want:  "network ls",
		},
		{
			name:  "volume_ls_passes_through",
			label: "imagetest.test=foo",
			args:  []string{"volume", "ls"},
			want:  "volume ls",
		},
		{
			name:  "global_flag_passes_through",
			label: "imagetest.test=foo",
			args:  []string{"--version"},
			want:  "--version",
		},
		{
			name:  "host_flag_passes_through",
			label: "imagetest.test=foo",
			args:  []string{"-H", "tcp://x:1234", "ps"},
			want:  "-H tcp://x:1234 ps",
		},
		{
			name:  "logs_passes_through",
			label: "imagetest.test=foo",
			args:  []string{"logs", "abc"},
			want:  "logs abc",
		},
		{
			name:  "exec_passes_through",
			label: "imagetest.test=foo",
			args:  []string{"exec", "-it", "abc", "sh"},
			want:  "exec -it abc sh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(shimFile, tc.args...)
			cmd.Env = append(os.Environ(), "IMAGETEST_TEST_LABEL="+tc.label)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("shim exec failed: %v\noutput: %s", err, out)
			}
			got := strings.TrimRight(string(out), "\n")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
