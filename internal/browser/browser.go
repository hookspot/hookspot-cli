// Package browser opens URLs in the user's default browser.
package browser

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

// command returns the platform opener for goos, or ok=false when goos has no
// supported opener. The URL is always a single argument, never a shell string.
func command(goos, target string) (name string, args []string, ok bool) {
	switch goos {
	case "darwin":
		return "open", []string{target}, true
	case "linux":
		return "xdg-open", []string{target}, true
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}, true
	default:
		return "", nil, false
	}
}

// Open starts the default browser at target and returns without waiting for it
// to exit.
func Open(target string) error {
	if !validBrowserURL(target) {
		return fmt.Errorf("refusing to open %q: only HTTP and HTTPS URLs are supported", target)
	}
	name, args, ok := command(runtime.GOOS, target)
	if !ok {
		return fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
	}
	if err := exec.Command(name, args...).Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func validBrowserURL(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}
