// Package endpoint validates and builds URLs for a fixed Hookspot deployment.
package endpoint

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Base is a validated Hookspot deployment URL.
type Base struct {
	url url.URL
}

// Parse validates a build-time deployment URL for environment.
func Parse(raw, environment string) (Base, error) {
	if environment != "dev" && environment != "prod" {
		return Base{}, fmt.Errorf("unknown build environment %q", environment)
	}
	if raw == "" {
		return Base{}, fmt.Errorf("%s server URL is empty", environment)
	}
	if strings.Contains(raw, "#") {
		return Base{}, fmt.Errorf("invalid %s server URL: fragments are not allowed", environment)
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return Base{}, fmt.Errorf("invalid %s server URL: whitespace and control characters are not allowed", environment)
		}
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Base{}, fmt.Errorf("invalid %s server URL", environment)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Base{}, fmt.Errorf("invalid %s server URL: scheme must be HTTP or HTTPS", environment)
	}
	if environment != "dev" && u.Scheme != "https" {
		return Base{}, fmt.Errorf("invalid %s server URL: HTTPS is required", environment)
	}
	if u.Opaque != "" || u.Host == "" || u.Hostname() == "" {
		return Base{}, fmt.Errorf("invalid %s server URL: host is required", environment)
	}
	host, ok := canonicalHostname(u.Hostname(), strings.HasPrefix(u.Host, "["))
	if !ok {
		return Base{}, fmt.Errorf("invalid %s server URL: invalid host", environment)
	}
	if u.User != nil {
		return Base{}, fmt.Errorf("invalid %s server URL: user information is not allowed", environment)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return Base{}, fmt.Errorf("invalid %s server URL: query parameters are not allowed", environment)
	}
	if u.Fragment != "" {
		return Base{}, fmt.Errorf("invalid %s server URL: fragments are not allowed", environment)
	}
	if strings.Contains(u.EscapedPath(), "%") || u.RawPath != "" {
		return Base{}, fmt.Errorf("invalid %s server URL: encoded path segments are not allowed", environment)
	}
	if !validPath(u.Path) {
		return Base{}, fmt.Errorf("invalid %s server URL: unsafe path", environment)
	}
	if strings.HasSuffix(u.Host, ":") {
		return Base{}, fmt.Errorf("invalid %s server URL: invalid port", environment)
	}
	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return Base{}, fmt.Errorf("invalid %s server URL: invalid port", environment)
		}
		port = strconv.Itoa(value)
		if (u.Scheme == "https" && value == 443) || (u.Scheme == "http" && value == 80) {
			port = ""
		}
	}

	u.Scheme = strings.ToLower(u.Scheme)
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return Base{url: *u}, nil
}

// API returns a fresh URL below the deployment's API prefix. It returns nil
// for a zero Base or an unsafe relative path.
func (b Base) API(relativePath string) *url.URL {
	if b.url.Scheme == "" || !validRelativePath(relativePath) {
		return nil
	}
	u := b.url
	u.Path += "/" + strings.TrimPrefix(relativePath, "/")
	return &u
}

// WebSocket returns a fresh URL for the deployment's websocket endpoint.
func (b Base) WebSocket() *url.URL {
	u := b.API("cli/websocket")
	if u == nil {
		return nil
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	query := url.Values{}
	query.Set("vsn", "2.0.0")
	u.RawQuery = query.Encode()
	return u
}

// String returns the canonical base URL.
func (b Base) String() string {
	if b.url.Scheme == "" {
		return ""
	}
	return b.url.String()
}

// Segment validates an opaque backend identifier before it is used in a URL.
func Segment(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("path identifier is empty")
	}
	for _, r := range value {
		if !isASCIIAlphaNumeric(r) && r != '_' && r != '-' {
			return "", fmt.Errorf("path identifier contains unsupported characters")
		}
	}
	return value, nil
}

func validRelativePath(value string) bool {
	value = strings.TrimPrefix(value, "/")
	if value == "" || strings.Contains(value, "%") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if !validUnreservedSegment(segment) {
			return false
		}
	}
	return true
}

func validPath(value string) bool {
	if value == "" || value == "/" {
		return true
	}
	if !strings.HasPrefix(value, "/") {
		return false
	}
	if value != "/" && strings.Contains(value, "//") {
		return false
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(value, "/"), "/")
	if trimmed == "" {
		return true
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if !validUnreservedSegment(segment) {
			return false
		}
	}
	return true
}

func validUnreservedSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !isASCIIAlphaNumeric(r) && !strings.ContainsRune("-._~", r) {
			return false
		}
	}
	return true
}

func isASCIIAlphaNumeric(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func canonicalHostname(host string, bracketed bool) (string, bool) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), true
	}
	if bracketed {
		return "", false
	}
	if len(host) > 253 {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, r := range label {
			if !isASCIIAlphaNumeric(r) && r != '-' {
				return "", false
			}
		}
	}
	return strings.ToLower(host), true
}
