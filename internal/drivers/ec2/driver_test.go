package ec2

import (
	"strings"
	"testing"
)

func TestSanitizeAWSTagValue(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "valid string unchanged",
			input:  "hello-world",
			maxLen: 256,
			want:   "hello-world",
		},
		{
			name:   "github bot actor",
			input:  "dependabot[bot]",
			maxLen: 256,
			want:   "dependabotbot",
		},
		{
			name:   "github actions bot",
			input:  "github-actions[bot]",
			maxLen: 256,
			want:   "github-actionsbot",
		},
		{
			name:   "valid special characters preserved",
			input:  "user_name.test:value/path=eq+plus-dash@email",
			maxLen: 256,
			want:   "user_name.test:value/path=eq+plus-dash@email",
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
			maxLen: 256,
			want:   "",
		},
		{
			name:   "only invalid characters",
			input:  "[]{}()",
			maxLen: 256,
			want:   "",
		},
		{
			name:   "mixed valid and invalid",
			input:  "hello[world]test{foo}bar",
			maxLen: 256,
			want:   "helloworldtestfoobar",
		},
		{
			name:   "unicode letters allowed",
			input:  "héllo-wörld",
			maxLen: 256,
			want:   "héllo-wörld",
		},
		{
			name:   "whitespace preserved",
			input:  "hello world",
			maxLen: 256,
			want:   "hello world",
		},
		{
			name:   "numbers allowed",
			input:  "test123",
			maxLen: 256,
			want:   "test123",
		},
		{
			name:   "rfc3339 timestamp valid",
			input:  "2025-12-11T10:30:00Z",
			maxLen: 256,
			want:   "2025-12-11T10:30:00Z",
		},
		{
			name:   "rfc3339 with timezone offset",
			input:  "2025-12-11T10:30:00-08:00",
			maxLen: 256,
			want:   "2025-12-11T10:30:00-08:00",
		},
		{
			name:   "typical github repository",
			input:  "chainguard-dev/terraform-provider-imagetest",
			maxLen: 256,
			want:   "chainguard-dev/terraform-provider-imagetest",
		},
		{
			name:   "tag key max length",
			input:  strings.Repeat("a", 150),
			maxLen: 128,
			want:   strings.Repeat("a", 128),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAWSTagValue(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("sanitizeAWSTagValue(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
