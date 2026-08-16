package proxy

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"
)

// Forwarder forwards received event bytes to a local target via HTTP POST.
type Forwarder struct {
	targetBaseURL string
	client        *http.Client
}

// New returns a Forwarder that sends requests to targetBaseURL.
func New(targetBaseURL string) *Forwarder {
	return &Forwarder{
		targetBaseURL: strings.TrimRight(targetBaseURL, "/"),
		client:        &http.Client{Timeout: 30 * time.Second},
	}
}

// ForwardURL returns the exact URL used to forward a delivery to targetBaseURL
// and its destination path.
func ForwardURL(targetBaseURL, destinationPath string) string {
	if !strings.HasPrefix(destinationPath, "/") {
		destinationPath = "/" + destinationPath
	}
	return strings.TrimRight(targetBaseURL, "/") + destinationPath
}

// Forward replays a request to targetBaseURL+path with the given method, raw
// query string, body, and headers. An empty method defaults to POST.
func (f *Forwarder) Forward(ctx context.Context, method, path, query string, body []byte, headers http.Header) (*http.Response, error) {
	if method == "" {
		method = http.MethodPost
	}

	target := ForwardURL(f.targetBaseURL, path)
	if query != "" {
		target += "?" + query
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	return f.client.Do(req)
}
