package gke

import (
	"reflect"
	"strings"
	"testing"
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
