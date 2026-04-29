package gke

import (
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
