package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"hookspot/internal/endpoint"
)

// ForwardTimeout is how long Forward waits for the local target's response.
const ForwardTimeout = 30 * time.Second

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
	trailingSlash bool
	client        *http.Client
}

var barePort = regexp.MustCompile(`^[0-9]+(/.*)?$`)

// New returns a Forwarder that sends requests to targetBaseURL. A bare port,
// optionally with a path, targets localhost.
func New(targetBaseURL string) (*Forwarder, error) {
	if targetBaseURL == "" {
		return nil, errors.New("forward target is empty")
	}
	if barePort.MatchString(targetBaseURL) {
		targetBaseURL = "localhost:" + targetBaseURL
	}
	if !strings.Contains(targetBaseURL, "://") {
		targetBaseURL = "http://" + targetBaseURL
	}
	base, err := endpoint.Parse(targetBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid forward target: %w", err)
	}

	// Docker injects HTTP_PROXY into containers; it must not capture the local hop.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil

	return &Forwarder{
		targetBaseURL: base.String(),
		trailingSlash: strings.HasSuffix(targetBaseURL, "/"),
		client: &http.Client{
			Transport: transport,
			Timeout:   ForwardTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// String returns the validated target base URL, with the trailing slash kept
// when typed, so a command built from it forwards the same way.
func (f *Forwarder) String() string {
	if f.trailingSlash {
		return f.targetBaseURL + "/"
	}
	return f.targetBaseURL
}

// DestinationURL returns the exact URL used for a delivery.
func (f *Forwarder) DestinationURL(destinationPath, rawQuery string) (*url.URL, error) {
	if strings.ContainsAny(destinationPath, "?#") {
		return nil, errors.New("invalid destination path")
	}
	for _, r := range destinationPath {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return nil, errors.New("invalid destination path")
		}
	}
	if !strings.HasPrefix(destinationPath, "/") {
		destinationPath = "/" + destinationPath
	}
	// A root destination is the --forward-to URL itself, slash only if typed.
	if destinationPath == "/" && !f.trailingSlash {
		destinationPath = ""
	}
	target, err := url.Parse(f.targetBaseURL + destinationPath)
	if err != nil {
		return nil, errors.New("invalid destination path")
	}
	if target.Path == "" {
		target.Path = "/"
	}
	target.RawQuery = rawQuery
	return target, nil
}

// Forward replays a request to targetBaseURL+path with the given method, raw
// query string, body, and headers. An empty method defaults to POST.
func (f *Forwarder) Forward(ctx context.Context, method, path, query string, body []byte, headers http.Header) (*http.Response, error) {
	if method == "" {
		method = http.MethodPost
	}

	target, err := f.DestinationURL(path, query)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	for key, values := range Headers(headers) {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	return f.client.Do(req)
}

// Headers returns the headers Forward sends: canonical names, without
// hop-by-hop headers or Host.
func Headers(headers http.Header) http.Header {
	forwarded := canonicalHeaders(headers)
	removeHopByHopHeaders(forwarded)
	return forwarded
}

func canonicalHeaders(headers http.Header) http.Header {
	canonical := make(http.Header, len(headers))
	for name, values := range headers {
		name = http.CanonicalHeaderKey(name)
		canonical[name] = append(canonical[name], values...)
	}
	return canonical
}

func removeHopByHopHeaders(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "Host",
	} {
		headers.Del(name)
	}
}
