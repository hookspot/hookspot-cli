package browser

import (
	"reflect"
	"testing"
)

func TestCommand(t *testing.T) {
	const target = "https://hookspot.localhost/cli/login/browser-token"
	tests := []struct {
		name string
		goos string
		want string
		args []string
		ok   bool
	}{
		{
			name: "darwin",
			goos: "darwin",
			want: "open",
			args: []string{target},
			ok:   true,
		},
		{
			name: "linux",
			goos: "linux",
			want: "xdg-open",
			args: []string{target},
			ok:   true,
		},
		{
			name: "windows",
			goos: "windows",
			want: "rundll32",
			args: []string{"url.dll,FileProtocolHandler", target},
			ok:   true,
		},
		{name: "unsupported goos", goos: "plan9"},
		{name: "empty goos", goos: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, args, ok := command(test.goos, target)
			if name != test.want || !reflect.DeepEqual(args, test.args) || ok != test.ok {
				t.Fatalf("command(%q) = %q, %q, %v; want %q, %q, %v", test.goos, name, args, ok, test.want, test.args, test.ok)
			}
		})
	}
}

// Open only ever reaches exec.Command for http and https URLs, so rejected
// schemes are safe to exercise without launching a browser.
func TestOpenRejectsUnsupportedURLs(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{name: "empty", target: ""},
		{name: "file", target: "file:///etc/passwd"},
		{name: "javascript", target: "javascript:alert(1)"},
		{name: "ftp", target: "ftp://example.com/file"},
		{name: "relative", target: "hookspot.localhost/cli/login/browser-token"},
		{name: "hostless", target: "http:cli/login"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Open(test.target); err == nil {
				t.Fatalf("Open(%q) succeeded", test.target)
			}
		})
	}
}
