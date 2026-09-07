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
		{"root", "https://stage.example.invalid", "stage", "https://stage.example.invalid/cli/me", "wss://stage.example.invalid/cli/websocket?vsn=2.0.0"},
		{"root trailing slash", "https://stage.example.invalid/", "stage", "https://stage.example.invalid/cli/me", "wss://stage.example.invalid/cli/websocket?vsn=2.0.0"},
		{"prefix", "https://stage.example.invalid/gateway/hookspot", "stage", "https://stage.example.invalid/gateway/hookspot/cli/me", "wss://stage.example.invalid/gateway/hookspot/cli/websocket?vsn=2.0.0"},
		{"prefix trailing slash", "https://stage.example.invalid/gateway/hookspot/", "stage", "https://stage.example.invalid/gateway/hookspot/cli/me", "wss://stage.example.invalid/gateway/hookspot/cli/websocket?vsn=2.0.0"},
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
	base, err := Parse("https://stage.example.invalid/prefix", "stage")
	if err != nil {
		t.Fatal(err)
	}
	first := base.API("cli/me")
	first.Path = "/changed"
	if got := base.API("cli/me").String(); got != "https://stage.example.invalid/prefix/cli/me" {
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
		{"empty", "", "dev"},
		{"release HTTP", "http://stage.example.invalid", "stage"},
		{"unsupported scheme", "ftp://example.invalid", "dev"},
		{"missing host", "https:///prefix", "stage"},
		{"invalid port", "https://example.invalid:99999", "stage"},
		{"userinfo", "https://credential-sentinel@example.invalid", "stage"},
		{"query", "https://example.invalid?key=credential-sentinel", "stage"},
		{"fragment", "https://example.invalid/#credential-sentinel", "stage"},
		{"empty fragment", "https://example.invalid#", "stage"},
		{"empty port", "https://example.invalid:", "stage"},
		{"whitespace", "https://example.invalid/a b", "stage"},
		{"control", "https://example.invalid/a\nb", "stage"},
		{"encoded separator", "https://example.invalid/a%2fb", "stage"},
		{"encoded dot", "https://example.invalid/%2e%2e", "stage"},
		{"empty path segment", "https://example.invalid/a//b", "stage"},
		{"repeated root separator", "https://example.invalid//", "stage"},
		{"dot segment", "https://example.invalid/a/../b", "stage"},
		{"unsafe path segment", "https://example.invalid/a;b", "stage"},
		{"bracketed DNS name", "https://[example.invalid]", "stage"},
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
