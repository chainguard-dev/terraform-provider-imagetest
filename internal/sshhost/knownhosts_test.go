package sshhost

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// A real ed25519 host key line, as ssh-keyscan prints it.
const testHostKeyLine = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGIx8mDY8HPYG2R+aYEKyQqLWQKCFoIk+aBF+I60wZqB"

func TestParseKnownHosts(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		host     string
		port     int32
		wantKeys int
		wantErr  string
	}{{
		name:     "bare host matches",
		data:     "192.0.2.10 " + testHostKeyLine + "\n",
		host:     "192.0.2.10",
		port:     22,
		wantKeys: 1,
	}, {
		name:     "bracketed host and port matches",
		data:     "[192.0.2.10]:2222 " + testHostKeyLine + "\n",
		host:     "192.0.2.10",
		port:     2222,
		wantKeys: 1,
	}, {
		name:     "comments and other hosts are skipped",
		data:     "# scanned\n192.0.2.99 " + testHostKeyLine + "\n192.0.2.10,example.com " + testHostKeyLine + "\n",
		host:     "192.0.2.10",
		port:     22,
		wantKeys: 1,
	}, {
		name:    "bracketed host with port 22 does not match, as in OpenSSH",
		data:    "[192.0.2.10]:22 " + testHostKeyLine + "\n",
		host:    "192.0.2.10",
		port:    22,
		wantErr: "no entry for 192.0.2.10",
	}, {
		name:    "bare host does not match on a non-default port, as in OpenSSH",
		data:    "192.0.2.10 " + testHostKeyLine + "\n",
		host:    "192.0.2.10",
		port:    2222,
		wantErr: "no entry for [192.0.2.10]:2222",
	}, {
		name:    "certificate authority entry is rejected",
		data:    "@cert-authority 192.0.2.10 " + testHostKeyLine + "\n",
		host:    "192.0.2.10",
		port:    22,
		wantErr: "certificate authority",
	}, {
		name:    "entry for another host only",
		data:    "192.0.2.99 " + testHostKeyLine + "\n",
		host:    "192.0.2.10",
		port:    22,
		wantErr: "no entry for 192.0.2.10",
	}, {
		name:    "revoked entry is ignored",
		data:    "@revoked 192.0.2.10 " + testHostKeyLine + "\n",
		host:    "192.0.2.10",
		port:    22,
		wantErr: "no entry for 192.0.2.10",
	}, {
		name:    "empty file",
		data:    "",
		host:    "192.0.2.10",
		port:    22,
		wantErr: "no entry for 192.0.2.10",
	}, {
		name:    "malformed line",
		data:    "192.0.2.10 ssh-ed25519 not-base64!\n",
		host:    "192.0.2.10",
		port:    22,
		wantErr: "parsing known_hosts",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := parseKnownHosts([]byte(tc.data), tc.host, tc.port)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseKnownHosts() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseKnownHosts() error = %v", err)
			}
			if len(keys) != tc.wantKeys {
				t.Fatalf("parseKnownHosts() returned %d keys, want %d", len(keys), tc.wantKeys)
			}
			want, _, _, _, err := ssh.ParseAuthorizedKey([]byte(testHostKeyLine))
			if err != nil {
				t.Fatalf("parsing fixture key: %v", err)
			}
			if string(keys[0].Marshal()) != string(want.Marshal()) {
				t.Errorf("parsed key does not match the fixture")
			}
		})
	}
}

// TestSSHOpts checks that the docker SSH options pin the host key only when
// a known_hosts file is configured.
func TestSSHOpts(t *testing.T) {
	tests := []struct {
		name       string
		knownHosts string
		want       []string
		wantNot    []string
	}{{
		name:    "unchecked by default",
		want:    []string{"StrictHostKeyChecking=no"},
		wantNot: []string{"UserKnownHostsFile="},
	}, {
		name:       "pinned with known_hosts",
		knownHosts: "/tmp/known_hosts",
		want:       []string{"UserKnownHostsFile=/tmp/known_hosts", "StrictHostKeyChecking=yes"},
		wantNot:    []string{"StrictHostKeyChecking=no"},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Host{Addr: "192.0.2.10", Port: 22, User: "ubuntu", KeyPath: "/tmp/key", KnownHosts: tc.knownHosts}

			opts := h.sshOpts()
			for _, w := range tc.want {
				if !slices.Contains(opts, w) {
					t.Errorf("sshOpts() = %v, missing %q", opts, w)
				}
			}
			for _, w := range tc.wantNot {
				if slices.ContainsFunc(opts, func(o string) bool { return strings.HasPrefix(o, w) }) {
					t.Errorf("sshOpts() = %v, must not contain %q", opts, w)
				}
			}
			if !slices.Contains(opts, "-i") || !slices.Contains(opts, "/tmp/key") {
				t.Errorf("sshOpts() = %v, missing identity file", opts)
			}
			if !slices.Contains(opts, "-l") || !slices.Contains(opts, "ubuntu") {
				t.Errorf("sshOpts() = %v, missing login user", opts)
			}
		})
	}
}
