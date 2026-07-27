package cmd

import "testing"

func TestForwardBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare host and port", "localhost:3000", "http://localhost:3000"},
		{"http url kept", "http://localhost:3000", "http://localhost:3000"},
		{"https url kept", "https://example.com/hooks", "https://example.com/hooks"},
		{"host only", "host.docker.internal", "http://host.docker.internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forwardBaseURL(tt.in); got != tt.want {
				t.Errorf("forwardBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
