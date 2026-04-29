package gke

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/chainguard-dev/clog"
)

func TestSanitizeGCPLabel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid string unchanged",
			input: "hello-world",
			want:  "hello-world",
		},
		{
			name:  "uppercase converted to lowercase",
			input: "Hello-World",
			want:  "hello-world",
		},
		{
			name:  "colons replaced with dashes",
			input: "imagetest:test-name",
			want:  "imagetest-test-name",
		},
		{
			name:  "spaces replaced with dashes",
			input: "my test name",
			want:  "my-test-name",
		},
		{
			name:  "truncated to 63 characters",
			input: strings.Repeat("a", 100),
			want:  strings.Repeat("a", 63),
		},
		{
			name:  "special characters replaced with dashes",
			input: "test[bot].foo@bar",
			want:  "test-bot--foo-bar",
		},
		{
			name:  "underscores preserved",
			input: "my_test_name",
			want:  "my_test_name",
		},
		{
			name:  "digits preserved",
			input: "test123",
			want:  "test123",
		},
		{
			name:  "digit leading character allowed (gcp key rule not enforced)",
			input: "1abc",
			want:  "1abc",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeGCPLabel(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeGCPLabel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestBuildLabels exercises (*driver).buildLabels, which composes the GKE
// resource labels by merging reserved imagetest labels with user-provided tags
// and passing dynamic inputs through sanitizeGCPLabel. This locks in the
// label-merging semantics — including the collision behavior between a user
// tag and a reserved key — so future changes to buildLabels can't silently
// regress them.
func TestBuildLabels(t *testing.T) {
	tests := []struct {
		name        string
		driverName  string
		clusterName string
		tags        map[string]string
		want        map[string]string
	}{
		{
			name:        "reserved labels only with nil tags",
			driverName:  "my-test",
			clusterName: "cluster-1",
			tags:        nil,
			want: map[string]string{
				"imagetest":              "true",
				"imagetest-test-name":    "my-test",
				"imagetest-cluster-name": "cluster-1",
			},
		},
		{
			name:        "reserved labels only with empty tags map",
			driverName:  "my-test",
			clusterName: "cluster-1",
			tags:        map[string]string{},
			want: map[string]string{
				"imagetest":              "true",
				"imagetest-test-name":    "my-test",
				"imagetest-cluster-name": "cluster-1",
			},
		},
		{
			name:        "reserved labels merged with clean user tags",
			driverName:  "my-test",
			clusterName: "cluster-1",
			tags: map[string]string{
				"team": "platform",
			},
			want: map[string]string{
				"imagetest":              "true",
				"imagetest-test-name":    "my-test",
				"imagetest-cluster-name": "cluster-1",
				"team":                   "platform",
			},
		},
		{
			name:        "user tag key and value sanitized through merge",
			driverName:  "my-test",
			clusterName: "cluster-1",
			tags: map[string]string{
				"Team Name": "Platform Eng",
			},
			want: map[string]string{
				"imagetest":              "true",
				"imagetest-test-name":    "my-test",
				"imagetest-cluster-name": "cluster-1",
				"team-name":              "platform-eng",
			},
		},
		{
			// Reserved labels are written into the map first, then user tags
			// are merged in via the range loop — so a user tag whose sanitized
			// key collides with a reserved key overwrites the reserved value.
			// This is documented current behavior, not necessarily desired:
			// changing it (e.g. to make reserved keys take priority, or to
			// reject colliding user keys) is a separate decision. This test
			// pins the behavior so any future change is explicit.
			name:        "user tag collides with reserved key — user value wins",
			driverName:  "my-test",
			clusterName: "cluster-1",
			tags: map[string]string{
				"imagetest": "false",
			},
			want: map[string]string{
				"imagetest":              "false",
				"imagetest-test-name":    "my-test",
				"imagetest-cluster-name": "cluster-1",
			},
		},
		{
			name:        "dynamic name and clusterName sanitized",
			driverName:  "My Test [Bot]",
			clusterName: "Cluster 1",
			tags:        nil,
			want: map[string]string{
				"imagetest":              "true",
				"imagetest-test-name":    "my-test--bot-",
				"imagetest-cluster-name": "cluster-1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &driver{
				name:        tt.driverName,
				clusterName: tt.clusterName,
				tags:        tt.tags,
			}
			got := k.buildLabels()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveProjectID exercises resolveProjectID — the project-ID resolver
// for the GKE driver. Locks in the precedence between the explicit Terraform
// value and the canonical GCP env vars, the deprecation warning for
// GOOGLE_PROJECT_ID, and whitespace trimming. Uses t.Setenv for hermetic
// env-var manipulation and a clog-bound bytes.Buffer to assert on the warning.
func TestResolveProjectID(t *testing.T) {
	const deprecationFragment = "GOOGLE_PROJECT_ID is deprecated"

	tests := []struct {
		name        string
		explicit    string
		envVars     map[string]string
		want        string
		wantWarning bool
	}{
		{
			name:     "explicit Terraform value wins over all env vars",
			explicit: "explicit",
			envVars: map[string]string{
				"GOOGLE_CLOUD_PROJECT": "primary",
				"GOOGLE_PROJECT_ID":    "deprecated",
			},
			want: "explicit",
		},
		{
			name:    "primary env var used",
			envVars: map[string]string{"GOOGLE_CLOUD_PROJECT": "primary"},
			want:    "primary",
		},
		{
			name:        "deprecated env var fallback fires warning",
			envVars:     map[string]string{"GOOGLE_PROJECT_ID": "deprecated"},
			want:        "deprecated",
			wantWarning: true,
		},
		{
			name: "primary wins when both env vars set, no warning",
			envVars: map[string]string{
				"GOOGLE_CLOUD_PROJECT": "primary",
				"GOOGLE_PROJECT_ID":    "deprecated",
			},
			want: "primary",
		},
		{
			name: "all sources unset returns empty",
			want: "",
		},
		{
			name:     "whitespace trimmed from explicit value",
			explicit: "  ws-proj  ",
			want:     "ws-proj",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all three env vars first so leakage from the host environment
			// or earlier subtests can't influence the result. t.Setenv restores
			// the original values after the test.
			for _, k := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_PROJECT_ID"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Bind a buffered logger to the context so we can assert on warnings.
			buf := &bytes.Buffer{}
			ctx := clog.WithLogger(context.Background(), clog.New(slog.NewTextHandler(buf, nil)))

			got := resolveProjectID(ctx, tt.explicit)
			if got != tt.want {
				t.Errorf("resolveProjectID(_, %q) = %q, want %q", tt.explicit, got, tt.want)
			}

			gotWarning := strings.Contains(buf.String(), deprecationFragment)
			if gotWarning != tt.wantWarning {
				t.Errorf("warning fired = %v, want %v\nlog output: %q", gotWarning, tt.wantWarning, buf.String())
			}
		})
	}
}
