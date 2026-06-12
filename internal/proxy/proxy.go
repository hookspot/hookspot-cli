package proxy

import (
	"bytes"
	"context"
	"net/http"
	"strings"
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
		client:        http.DefaultClient,
	}
}

// Forward sends body as an HTTP POST to targetBaseURL+path, copying the
// given headers onto the outgoing request.
func (f *Forwarder) Forward(ctx context.Context, path string, body []byte, headers http.Header) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.targetBaseURL+path, bytes.NewReader(body))
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
