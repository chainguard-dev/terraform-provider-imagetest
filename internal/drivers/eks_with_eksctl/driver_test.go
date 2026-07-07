package ekswitheksctl

import (
	"bytes"
	"strings"
	"testing"
)

func TestTail(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		max  int
		want string
	}{
		{
			name: "short output is unchanged",
			in:   []byte("all good"),
			max:  64,
			want: "all good",
		},
		{
			name: "exactly max is unchanged",
			in:   bytes.Repeat([]byte("x"), 8),
			max:  8,
			want: "xxxxxxxx",
		},
		{
			name: "long output keeps the tail and notes truncation",
			in:   []byte(strings.Repeat("noise\n", 100) + "the actual error"),
			max:  16,
			want: "[... 600 bytes truncated ...]\nthe actual error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(tail(tt.in, tt.max)); got != tt.want {
				t.Errorf("tail() = %q, want %q", got, tt.want)
			}
		})
	}
}
