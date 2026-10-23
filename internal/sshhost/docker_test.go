package sshhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
)

// TestPullsOnHost checks that only images in private ECR registries are
// pulled on the host, and only when the host is set up for it.
func TestPullsOnHost(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		set  bool
		want bool
	}{{
		name: "ECR",
		ref:  "123456789012.dkr.ecr.us-west-2.amazonaws.com/imagetest:latest",
		set:  true,
		want: true,
	}, {
		name: "ECR FIPS",
		ref:  "123456789012.dkr.ecr-fips.us-gov-west-1.amazonaws.com/imagetest",
		set:  true,
		want: true,
	}, {
		name: "ECR China",
		ref:  "123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn/imagetest",
		set:  true,
		want: true,
	}, {
		name: "ECR without host credentials",
		ref:  "123456789012.dkr.ecr.us-west-2.amazonaws.com/imagetest",
	}, {
		name: "ECR Public",
		ref:  "public.ecr.aws/chainguard/wolfi-base",
		set:  true,
	}, {
		name: "other registry",
		ref:  "cgr.dev/chainguard/wolfi-base",
		set:  true,
	}, {
		name: "short account ID",
		ref:  "12345.dkr.ecr.us-west-2.amazonaws.com/imagetest",
		set:  true,
	}, {
		name: "ECR look-alike",
		ref:  "123456789012.dkr.ecr.us-west-2.amazonaws.com.example.com/imagetest",
		set:  true,
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := name.ParseReference(tc.ref)
			if err != nil {
				t.Fatal(err)
			}
			h := &Host{PullECRWithHostCredentials: tc.set}
			if got := h.pullsOnHost(ref); got != tc.want {
				t.Errorf("pullsOnHost(%s) = %t, want %t", tc.ref, got, tc.want)
			}
		})
	}
}

// TestHostPullScript runs the host pull script under sh with a stand-in
// docker CLI. The CLI must see a config that maps only the image's registry
// to the ECR helper and DOCKER_HOST set to the endpoint of the user's
// current context, which the script must read with the user's own config,
// and the config must be gone afterwards, whether the pull succeeds or
// fails. When the inspect fails, nothing is pulled and nothing is left
// behind.
func TestHostPullScript(t *testing.T) {
	ref, err := name.ParseReference("123456789012.dkr.ecr.us-west-2.amazonaws.com/imagetest@sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	script, err := hostPullScript(ref)
	if err != nil {
		t.Fatalf("hostPullScript() = %v", err)
	}

	for _, tc := range []struct {
		name        string
		inspectExit int
		exit        int
		wantErr     bool
	}{
		{name: "pull succeeds"},
		{name: "pull fails", exit: 1, wantErr: true},
		{name: "inspect fails", inspectExit: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			tmp := t.TempDir()
			out := filepath.Join(tmp, "out")
			ctx := filepath.Join(tmp, "ctx")
			docker := strings.Join([]string{
				"#!/bin/sh",
				`if [ "$1" = context ]; then`,
				`  printf '%s\n' "${DOCKER_CONFIG-unset}" >"$CTX"`,
				`  printf '%s\n' "$*" >>"$CTX"`,
				`  echo unix:///run/user/1001/docker.sock`,
				"  exit " + strconv.Itoa(tc.inspectExit),
				"fi",
				`printf '%s\n' "$DOCKER_CONFIG" >"$OUT"`,
				`cat "$DOCKER_CONFIG/config.json" >>"$OUT"`,
				`printf '%s\n' "$*" >>"$OUT"`,
				`printf '%s\n' "$DOCKER_HOST" >>"$OUT"`,
				"exit " + strconv.Itoa(tc.exit),
			}, "\n") + "\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(docker), 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := exec.CommandContext(t.Context(), "sh")
			cmd.Stdin = strings.NewReader(strings.Join(script, "\n") + "\n")
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"OUT="+out, "CTX="+ctx, "TMPDIR="+tmp)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantErr {
				t.Fatalf("script error = %v, want error %t, output:\n%s", err, tc.wantErr, output)
			}

			data, err := os.ReadFile(ctx)
			if err != nil {
				t.Fatalf("docker context inspect did not run: %v", err)
			}
			if want := "unset\ncontext inspect --format {{.Endpoints.docker.Host}}"; strings.TrimSpace(string(data)) != want {
				t.Errorf("context inspect saw %q, want %q", strings.TrimSpace(string(data)), want)
			}

			entries, err := os.ReadDir(tmp)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.IsDir() {
					t.Errorf("config dir %s still exists", e.Name())
				}
			}

			if tc.inspectExit != 0 {
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Errorf("docker pull ran after a failed inspect (stat error %v)", err)
				}
				return
			}

			data, err = os.ReadFile(out)
			if err != nil {
				t.Fatalf("docker did not run: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 4 {
				t.Fatalf("docker recorded %q, want config dir, config, arguments and DOCKER_HOST", lines)
			}
			if want := `{"credHelpers":{"123456789012.dkr.ecr.us-west-2.amazonaws.com":"ecr-login"}}`; lines[1] != want {
				t.Errorf("config = %s, want %s", lines[1], want)
			}
			if want := "pull --quiet " + ref.Name(); lines[2] != want {
				t.Errorf("arguments = %q, want %q", lines[2], want)
			}
			if want := "unix:///run/user/1001/docker.sock"; lines[3] != want {
				t.Errorf("DOCKER_HOST = %q, want %q", lines[3], want)
			}
		})
	}
}
