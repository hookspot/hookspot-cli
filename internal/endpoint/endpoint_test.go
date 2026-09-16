package endpoint

import (
	"strings"
	"testing"
)

func TestParseBuildsCanonicalRoutes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		env  string
		api  string
		ws   string
	}{
		{"root", "https://prod.example.invalid", "prod", "https://prod.example.invalid/cli/me", "wss://prod.example.invalid/cli/websocket?vsn=2.0.0"},
		{"root trailing slash", "https://prod.example.invalid/", "prod", "https://prod.example.invalid/cli/me", "wss://prod.example.invalid/cli/websocket?vsn=2.0.0"},
		{"prefix", "https://prod.example.invalid/gateway/hookspot", "prod", "https://prod.example.invalid/gateway/hookspot/cli/me", "wss://prod.example.invalid/gateway/hookspot/cli/websocket?vsn=2.0.0"},
		{"prefix trailing slash", "https://prod.example.invalid/gateway/hookspot/", "prod", "https://prod.example.invalid/gateway/hookspot/cli/me", "wss://prod.example.invalid/gateway/hookspot/cli/websocket?vsn=2.0.0"},
		{"development HTTP", "http://127.0.0.1:4000", "dev", "http://127.0.0.1:4000/cli/me", "ws://127.0.0.1:4000/cli/websocket?vsn=2.0.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, err := Parse(test.raw, test.env)
			if err != nil {
				t.Fatal(err)
			}
			if got := base.API("cli/me"); got == nil || got.String() != test.api {
				t.Fatalf("API URL = %v, want %q", got, test.api)
			}
			if got := base.WebSocket(); got == nil || got.String() != test.ws {
				t.Fatalf("WebSocket URL = %v, want %q", got, test.ws)
			}
		})
	}
}

func TestBaseReturnsIndependentURLs(t *testing.T) {
	base, err := Parse("https://prod.example.invalid/prefix", "prod")
	if err != nil {
		t.Fatal(err)
	}
	first := base.API("cli/me")
	first.Path = "/changed"
	if got := base.API("cli/me").String(); got != "https://prod.example.invalid/prefix/cli/me" {
		t.Fatalf("second API URL = %q", got)
	}
}

func TestParseCanonicalizesEquivalentHostsAndDefaultPorts(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"https://EXAMPLE.invalid", "https://example.invalid"},
		{"https://example.invalid:443", "https://example.invalid"},
		{"https://example.invalid:0443", "https://example.invalid"},
		{"http://example.invalid:80", "http://example.invalid"},
		{"http://example.invalid:0080", "http://example.invalid"},
		{"https://[2001:0db8:0:0:0:0:0:1]:443", "https://[2001:db8::1]"},
	}
	for _, test := range tests {
		base, err := Parse(test.raw, "dev")
		if err != nil {
			t.Fatalf("Parse(%q): %v", test.raw, err)
		}
		if got := base.String(); got != test.want {
			t.Fatalf("Parse(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestParseRejectsUnsafeInputsWithoutEchoingThem(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		env  string
	}{
		{"unknown environment", "https://example.invalid", "qa"},
		{"stage environment", "https://example.invalid", "stage"},
		{"empty", "", "dev"},
		{"release HTTP", "http://prod.example.invalid", "prod"},
		{"unsupported scheme", "ftp://example.invalid", "dev"},
		{"missing host", "https:///prefix", "prod"},
		{"invalid port", "https://example.invalid:99999", "prod"},
		{"userinfo", "https://credential-sentinel@example.invalid", "prod"},
		{"query", "https://example.invalid?key=credential-sentinel", "prod"},
		{"fragment", "https://example.invalid/#credential-sentinel", "prod"},
		{"empty fragment", "https://example.invalid#", "prod"},
		{"empty port", "https://example.invalid:", "prod"},
		{"whitespace", "https://example.invalid/a b", "prod"},
		{"control", "https://example.invalid/a\nb", "prod"},
		{"encoded separator", "https://example.invalid/a%2fb", "prod"},
		{"encoded dot", "https://example.invalid/%2e%2e", "prod"},
		{"empty path segment", "https://example.invalid/a//b", "prod"},
		{"repeated root separator", "https://example.invalid//", "prod"},
		{"dot segment", "https://example.invalid/a/../b", "prod"},
		{"unsafe path segment", "https://example.invalid/a;b", "prod"},
		{"bracketed DNS name", "https://[example.invalid]", "prod"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.raw, test.env)
			if err == nil {
				t.Fatal("Parse unexpectedly succeeded")
			}
			if strings.Contains(err.Error(), "credential-sentinel") {
				t.Fatalf("error leaked input: %v", err)
			}
		})
	}
}

func TestSegment(t *testing.T) {
	for _, value := range []string{"abc", "ABC-123_test"} {
		if got, err := Segment(value); err != nil || got != value {
			t.Fatalf("Segment(%q) = %q, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "a/b", "a%2fb", "a.b", "two words", "ü"} {
		if _, err := Segment(value); err == nil {
			t.Fatalf("Segment(%q) unexpectedly succeeded", value)
		}
	}
}

func TestZeroBaseDoesNotBuildURLs(t *testing.T) {
	var base Base
	if base.API("cli/me") != nil || base.WebSocket() != nil {
		t.Fatal("zero Base built a URL")
	}
}
