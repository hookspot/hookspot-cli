package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// TransportErrorKind identifies common failures that prevent a request from
// receiving an HTTP response.
type TransportErrorKind string

const (
	TransportConnectionRefused TransportErrorKind = "connection_refused"
	TransportTimeout           TransportErrorKind = "timeout"
	TransportDNS               TransportErrorKind = "dns"
	TransportTLS               TransportErrorKind = "tls"
	TransportOther             TransportErrorKind = "transport_error"
)

// TransportFailure retains the original error while exposing a stable class
// suitable for an actionable terminal message.
type TransportFailure struct {
	Kind TransportErrorKind
	Err  error
}

func (f *TransportFailure) Error() string {
	if f == nil || f.Err == nil {
		return "transport error"
	}
	return f.Err.Error()
}

func (f *TransportFailure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Err
}

// ClassifyTransportError walks wrapped network errors and returns their most
// useful user-facing category.
func ClassifyTransportError(err error) TransportErrorKind {
	if err == nil {
		return TransportOther
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return TransportTimeout
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return TransportTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return TransportConnectionRefused
	}

	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return TransportDNS
	}

	var recordHeaderError tls.RecordHeaderError
	var alertError tls.AlertError
	var certificateVerificationError *tls.CertificateVerificationError
	var unknownAuthorityError x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateInvalidError x509.CertificateInvalidError
	var systemRootsError x509.SystemRootsError
	if errors.As(err, &recordHeaderError) ||
		errors.As(err, &alertError) ||
		errors.As(err, &certificateVerificationError) ||
		errors.As(err, &unknownAuthorityError) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &certificateInvalidError) ||
		errors.As(err, &systemRootsError) {
		return TransportTLS
	}

	return TransportOther
}

// Failure wraps err with its transport category.
func Failure(err error) *TransportFailure {
	if err == nil {
		return nil
	}
	var failure *TransportFailure
	if errors.As(err, &failure) {
		return failure
	}
	return &TransportFailure{Kind: ClassifyTransportError(err), Err: err}
}

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
