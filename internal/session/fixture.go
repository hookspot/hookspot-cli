package session

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"hookspot/internal/ws"
)

// fixtureDir is where exports land, relative to the working directory.
const fixtureDir = "hookspot-fixtures"

// redactedValue replaces a sensitive header's values.
const redactedValue = "[redacted]"

// plainName matches a string that is safe as a file name and a bare shell word.
var plainName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Fixture is a request exported as files.
type Fixture struct {
	// JSON and Body are the absolute paths written.
	JSON, Body string
	// Redacted is set when sensitive header values were left out.
	Redacted bool
}

// ExportFixture writes entry n as hookspot-fixtures/<name>.json, its method,
// path, query and headers, and <name>.body, its raw body. Sensitive header
// values are redacted when redact is set.
func (s *Session) ExportFixture(n int, redact bool) (Fixture, error) {
	entry, err := s.entry(n)
	if err != nil {
		return Fixture{}, err
	}
	d := entry.Delivery
	headers, redacted := redactHeaders(d.Headers, redact)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(struct {
		Method  string      `json:"method"`
		Path    string      `json:"path"`
		Query   string      `json:"query"`
		Headers http.Header `json:"headers"`
	}{deliveryMethod(d), d.Path, d.Query, headers}); err != nil {
		return Fixture{}, err
	}

	name := fixtureName(entry)
	fixture := Fixture{Redacted: redacted}
	if fixture.JSON, err = writeFixture(name+".json", encoded.Bytes()); err != nil {
		return Fixture{}, err
	}
	if fixture.Body, err = writeFixture(name+".body", d.Body); err != nil {
		return Fixture{}, err
	}
	return fixture, nil
}

// SensitiveHeader reports whether a header carries credentials, whose values
// stay hidden unless --show-sensitive-headers.
func SensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie",
		"x-cli-key", "x-api-key", "api-key", "x-hookspot-cli-key":
		return true
	default:
		return false
	}
}

// redactHeaders returns headers with sensitive values replaced when redact is
// set, and whether any were.
func redactHeaders(headers http.Header, redact bool) (http.Header, bool) {
	out := make(http.Header, len(headers))
	redacted := false
	for name, values := range headers {
		if redact && SensitiveHeader(name) {
			values, redacted = slices.Repeat([]string{redactedValue}, len(values)), true
		}
		out[name] = values
	}
	return out, redacted
}

// fixtureName names entry's files after its request UID, so exporting a
// request again replaces its files, or entry-<N> when the UID isn't a safe
// file name.
func fixtureName(entry Entry) string {
	if plainName.MatchString(entry.Delivery.RequestUID) {
		return entry.Delivery.RequestUID
	}
	return "entry-" + strconv.Itoa(entry.Number)
}

// writeFixture writes file into hookspot-fixtures and returns its absolute
// path. Requests carry credentials and personal data, so only the owner can
// read it.
func writeFixture(file string, data []byte) (string, error) {
	dir, err := filepath.Abs(fixtureDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, file)
	return path, os.WriteFile(path, data, 0o600)
}

func deliveryMethod(d ws.Delivery) string {
	if d.Method == "" {
		return http.MethodPost
	}
	return d.Method
}
