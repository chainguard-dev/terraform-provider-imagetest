package gce

import (
	"strings"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

func TestSanitizeGCELabelValue(t *testing.T) {
	// GCE label constraints:
	// https://cloud.google.com/compute/docs/labeling-resources#requirements
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "valid string unchanged",
			input:  "hello-world",
			maxLen: 63,
			want:   "hello-world",
		},
		{
			name:   "uppercase converted to lowercase",
			input:  "Hello-World",
			maxLen: 63,
			want:   "hello-world",
		},
		{
			name:   "special characters replaced with hyphens",
			input:  "user.name:value/path",
			maxLen: 63,
			want:   "user-name-value-path",
		},
		{
			name:   "truncate to max length",
			input:  "abcdefghij",
			maxLen: 5,
			want:   "abcde",
		},
		{
			name:   "empty string",
			input:  "",
			maxLen: 63,
			want:   "",
		},
		{
			name:   "underscores preserved",
			input:  "hello_world",
			maxLen: 63,
			want:   "hello_world",
		},
		{
			name:   "numbers allowed",
			input:  "test123",
			maxLen: 63,
			want:   "test123",
		},
		{
			name:   "github bot actor",
			input:  "dependabot[bot]",
			maxLen: 63,
			want:   "dependabot-bot-",
		},
		{
			name:   "long string truncated to 63",
			input:  strings.Repeat("a", 100),
			maxLen: 63,
			want:   strings.Repeat("a", 63),
		},
		{
			name:   "unicode replaced with hyphens",
			input:  "héllo-wörld",
			maxLen: 63,
			want:   "h-llo-w-rld",
		},
		{
			name:   "starts with number is valid for values",
			input:  "123test",
			maxLen: 63,
			want:   "123test",
		},
		{
			name:   "rfc3339 timestamp",
			input:  "2025-12-11T10:30:00Z",
			maxLen: 63,
			want:   "2025-12-11t10-30-00z",
		},
		{
			name:   "typical github repository",
			input:  "chainguard-dev/terraform-provider-imagetest",
			maxLen: 63,
			want:   "chainguard-dev-terraform-provider-imagetest",
		},
		{
			name:   "all invalid characters",
			input:  "[]{}/\\",
			maxLen: 63,
			want:   "------",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeGCELabelValue(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("sanitizeGCELabelValue(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestSanitizeGCEName(t *testing.T) {
	// GCE resource name constraints:
	// https://cloud.google.com/compute/docs/naming-resources#resource-name-format
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid name unchanged",
			input: "imagetest-gce-abc12345",
			want:  "imagetest-gce-abc12345",
		},
		{
			name:  "uppercase converted",
			input: "ImageTest-GCE",
			want:  "imagetest-gce",
		},
		{
			name:  "invalid chars replaced",
			input: "test_name.foo",
			want:  "test-name-foo",
		},
		{
			name:  "starts with number gets prefix",
			input: "123test",
			want:  "i123test",
		},
		{
			name:  "starts with hyphen gets prefix",
			input: "-test",
			want:  "i-test",
		},
		{
			name:  "trailing hyphens stripped",
			input: "test---",
			want:  "test",
		},
		{
			name:  "long name truncated",
			input: strings.Repeat("a", 100),
			want:  strings.Repeat("a", 63),
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only hyphens after sanitize",
			input: "___",
			want:  "i",
		},
		{
			name:  "truncation then trailing hyphen trim",
			input: strings.Repeat("a", 62) + "--",
			want:  strings.Repeat("a", 62),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeGCEName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeGCEName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConfigApplyDefaults(t *testing.T) {
	t.Run("empty config gets defaults", func(t *testing.T) {
		cfg := Config{}
		cfg.applyDefaults()

		if cfg.MachineType != "n1-standard-4" {
			t.Errorf("MachineType = %q, want %q", cfg.MachineType, "n1-standard-4")
		}
		if cfg.RootDiskSizeGB != 50 {
			t.Errorf("RootDiskSizeGB = %d, want %d", cfg.RootDiskSizeGB, 50)
		}
		if cfg.RootDiskType != "pd-ssd" {
			t.Errorf("RootDiskType = %q, want %q", cfg.RootDiskType, "pd-ssd")
		}
		if cfg.SSHUser != "ubuntu" {
			t.Errorf("SSHUser = %q, want %q", cfg.SSHUser, "ubuntu")
		}
		if cfg.SSHPort != 22 {
			t.Errorf("SSHPort = %d, want %d", cfg.SSHPort, 22)
		}
		if cfg.Shell != "bash" {
			t.Errorf("Shell = %q, want %q", cfg.Shell, "bash")
		}
		if cfg.Env == nil {
			t.Error("Env should be initialized, got nil")
		}
	})

	t.Run("user values preserved", func(t *testing.T) {
		cfg := Config{
			MachineType:    "e2-medium",
			RootDiskSizeGB: 100,
			RootDiskType:   "pd-standard",
			SSHUser:        "admin",
			SSHPort:        2222,
			Shell:          "zsh",
			Env:            map[string]string{"FOO": "bar"},
		}
		cfg.applyDefaults()

		if cfg.MachineType != "e2-medium" {
			t.Errorf("MachineType = %q, want %q", cfg.MachineType, "e2-medium")
		}
		if cfg.RootDiskSizeGB != 100 {
			t.Errorf("RootDiskSizeGB = %d, want %d", cfg.RootDiskSizeGB, 100)
		}
		if cfg.RootDiskType != "pd-standard" {
			t.Errorf("RootDiskType = %q, want %q", cfg.RootDiskType, "pd-standard")
		}
		if cfg.SSHUser != "admin" {
			t.Errorf("SSHUser = %q, want %q", cfg.SSHUser, "admin")
		}
		if cfg.SSHPort != 2222 {
			t.Errorf("SSHPort = %d, want %d", cfg.SSHPort, 2222)
		}
		if cfg.Shell != "zsh" {
			t.Errorf("Shell = %q, want %q", cfg.Shell, "zsh")
		}
		if cfg.Env["FOO"] != "bar" {
			t.Errorf("Env[FOO] = %q, want %q", cfg.Env["FOO"], "bar")
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		cfg := Config{}
		cfg.applyDefaults()
		cfg.applyDefaults()

		if cfg.MachineType != "n1-standard-4" {
			t.Errorf("MachineType = %q after double apply", cfg.MachineType)
		}
	})
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "missing project_id",
			cfg:     Config{Zone: "us-west1-b", Network: "default", Image: "img"},
			wantErr: "project_id is required",
		},
		{
			name:    "missing zone",
			cfg:     Config{ProjectID: "my-project", Network: "default", Image: "img"},
			wantErr: "zone is required",
		},
		{
			name:    "missing network",
			cfg:     Config{ProjectID: "my-project", Zone: "us-west1-b", Image: "img"},
			wantErr: "network is required",
		},
		{
			name:    "missing image",
			cfg:     Config{ProjectID: "my-project", Zone: "us-west1-b", Network: "default"},
			wantErr: "image is required",
		},
		{
			name: "valid config",
			cfg:  Config{ProjectID: "my-project", Zone: "us-west1-b", Network: "default", Image: "img"},
		},
		{
			name:    "accelerator count without type",
			cfg:     Config{ProjectID: "p", Zone: "z", Network: "n", Image: "i", AcceleratorCount: 1},
			wantErr: "accelerator_type is required when accelerator_count > 0",
		},
		{
			name: "accelerator count with type",
			cfg:  Config{ProjectID: "p", Zone: "z", Network: "n", Image: "i", AcceleratorCount: 1, AcceleratorType: "nvidia-tesla-t4"},
		},
		{
			name: "existing instance valid",
			cfg:  Config{ExistingInstance: &ExistingInstance{IP: "1.2.3.4", SSHKey: "/tmp/key.pem"}},
		},
		{
			name:    "existing instance missing IP",
			cfg:     Config{ExistingInstance: &ExistingInstance{SSHKey: "/tmp/key.pem"}},
			wantErr: "existing_instance.ip is required",
		},
		{
			name:    "existing instance missing SSH key",
			cfg:     Config{ExistingInstance: &ExistingInstance{IP: "1.2.3.4"}},
			wantErr: "existing_instance.ssh_key is required",
		},
		{
			name: "existing instance skips other validation",
			cfg:  Config{ExistingInstance: &ExistingInstance{IP: "1.2.3.4", SSHKey: "/tmp/key.pem"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Errorf("validate() = nil, want error containing %q", tt.wantErr)
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestBuildLabels(t *testing.T) {
	labels := buildLabels("my-test-name")

	required := []string{"imagetest", "imagetest-driver", "imagetest-test-name", "imagetest-expires", "team", "project"}
	for _, key := range required {
		if _, ok := labels[key]; !ok {
			t.Errorf("buildLabels() missing required key %q", key)
		}
	}

	if labels["imagetest"] != "true" {
		t.Errorf("imagetest label = %q, want %q", labels["imagetest"], "true")
	}
	if labels["imagetest-driver"] != "gce" {
		t.Errorf("imagetest-driver label = %q, want %q", labels["imagetest-driver"], "gce")
	}

	// Verify all label keys and values contain only valid characters
	// https://cloud.google.com/compute/docs/labeling-resources#requirements
	for k, v := range labels {
		for _, r := range k {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				t.Errorf("label key %q contains invalid rune %q", k, string(r))
			}
		}
		for _, r := range v {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				t.Errorf("label value %q for key %q contains invalid rune %q", v, k, string(r))
			}
		}
	}
}

func TestMetadataEntry(t *testing.T) {
	keys, err := ssh.NewED25519KeyPair()
	if err != nil {
		t.Fatalf("generating key pair: %v", err)
	}

	sk := &sshKey{
		name:    "test-key",
		sshUser: "ubuntu",
		public:  keys.Public,
		private: keys.Private,
	}

	entry, err := sk.metadataEntry()
	if err != nil {
		t.Fatalf("metadataEntry() error = %v", err)
	}

	// Must be a single line (no embedded newlines)
	if strings.Contains(entry, "\n") {
		t.Errorf("metadataEntry() contains newline: %q", entry)
	}

	// Must start with "ubuntu:"
	if !strings.HasPrefix(entry, "ubuntu:") {
		t.Errorf("metadataEntry() doesn't start with 'ubuntu:': %q", entry)
	}

	// Must contain ssh-ed25519
	if !strings.Contains(entry, "ssh-ed25519") {
		t.Errorf("metadataEntry() doesn't contain 'ssh-ed25519': %q", entry)
	}

	// Must end with " imagetest"
	if !strings.HasSuffix(entry, " imagetest") {
		t.Errorf("metadataEntry() doesn't end with ' imagetest': %q", entry)
	}

	// Verify format: "user:ssh-ed25519 AAAA... imagetest" (3 space-separated fields after colon)
	parts := strings.SplitN(entry, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("metadataEntry() should have user:key format, got: %q", entry)
	}
	if parts[0] != "ubuntu" {
		t.Errorf("user part = %q, want %q", parts[0], "ubuntu")
	}

	keyParts := strings.Fields(parts[1])
	if len(keyParts) != 3 {
		t.Errorf("key part should have 3 space-separated fields (type, key, comment), got %d: %q", len(keyParts), parts[1])
	}
	if keyParts[0] != "ssh-ed25519" {
		t.Errorf("key type = %q, want %q", keyParts[0], "ssh-ed25519")
	}
	if keyParts[2] != "imagetest" {
		t.Errorf("comment = %q, want %q", keyParts[2], "imagetest")
	}
}
