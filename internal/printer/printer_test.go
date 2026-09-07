package printer

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func fixed(t *testing.T, p *Printer) {
	t.Helper()
	p.now = func() time.Time {
		return time.Date(2026, 7, 12, 12, 34, 56, 789_000_000, time.UTC)
	}
}

func delivery() ws.Delivery {
	return ws.Delivery{
		AttemptUID: "att-internal-never-print",
		RequestUID: "req_01JFULLREQUESTUID",
		SourceUID:  "src_stripe",
		Method:     "POST",
		Path:       "/api/webhooks",
		Query:      "page=1&x=2",
		Headers: http.Header{
			"stripe-signature": []string{"t=1690,v1=5f8e"},
			"Content-Type":     []string{"application/json; charset=utf-8"},
			"Authorization":    []string{"Bearer secret"},
		},
		Body: []byte(`{"id":"evt_1","type":"payment_intent.succeeded"}`),
	}
}

func newTestPrinter(t *testing.T, out *bytes.Buffer, limits Limits) *Printer {
	t.Helper()
	p := New(out, Options{
		Sources: map[string]string{
			"src_stripe":  "stripe",
			"src_shopify": "shopify",
		},
		Limits: limits,
	})
	fixed(t, p)
	return p
}

func TestInspectGolden(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()

	response, err := p.Handle(d)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if response.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Status)
	}

	want := fmt.Sprintf(
		"12:34:56.789  ● stripe   POST  /api/webhooks  id req_01JFULLREQUESTUID\n"+
			"├─ query       page=1&x=2\n"+
			"│\n"+
			"├─ headers     authorization: (redacted)\n"+
			"│              content-type: application/json; charset=utf-8\n"+
			"│              stripe-signature: t=1690,v1=5f8e\n"+
			"│\n"+
			"└─ body        application/json · %s\n"+
			"    {\n"+
			"      \"id\": \"evt_1\",\n"+
			"      \"type\": \"payment_intent.succeeded\"\n"+
			"    }\n\n",
		formatBytes(len(d.Body)),
	)
	if got := output.String(); got != want {
		t.Fatalf("inspect output:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(output.String(), d.AttemptUID) {
		t.Fatal("inspect output exposed attempt UID")
	}
}

func TestInspectReturnsWriterFailureWithoutAcknowledgement(t *testing.T) {
	wantErr := errors.New("output unavailable")
	p := New(errorWriter{err: wantErr}, Options{})
	response, err := p.Handle(delivery())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle error = %v, want writer failure", err)
	}
	if response.Status != 0 {
		t.Fatalf("response status = %d, want no acknowledgement", response.Status)
	}
}

func TestForwardReturnsWriterFailure(t *testing.T) {
	wantErr := errors.New("output unavailable")
	p := New(errorWriter{err: wantErr}, Options{})
	err := p.PrintForward(delivery(), ForwardOutcome{Response: ws.Response{Status: http.StatusTemporaryRedirect}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("PrintForward error = %v, want writer failure", err)
	}
}

func TestInspectShowsSensitiveHeadersOnRequest(t *testing.T) {
	var output bytes.Buffer
	p := New(&output, Options{
		Sources:              map[string]string{"src_stripe": "stripe"},
		ShowSensitiveHeaders: true,
	})
	fixed(t, p)
	d := delivery()
	d.Query = ""
	d.Body = nil

	_, _ = p.Handle(d)

	if !strings.Contains(output.String(), "authorization: Bearer secret") {
		t.Fatalf("output did not contain sensitive header:\n%s", output.String())
	}
	if strings.Contains(output.String(), "query") {
		t.Fatalf("empty query section was printed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "└─ body        (empty)") {
		t.Fatalf("empty body marker missing:\n%s", output.String())
	}
}

func TestInspectRedactsCredentialHeaderNamesByDefault(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()
	d.Body = nil
	d.Headers = http.Header{
		"X-CLI-Key":          []string{"cli-sentinel"},
		"X-API-Key":          []string{"api-sentinel"},
		"X-Hookspot-CLI-Key": []string{"hookspot-sentinel"},
	}
	if _, err := p.Handle(d); err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{"cli-sentinel", "api-sentinel", "hookspot-sentinel"} {
		if strings.Contains(output.String(), sentinel) {
			t.Fatalf("output exposed credential header value %q", sentinel)
		}
	}
	if got := strings.Count(output.String(), "(redacted)"); got != 3 {
		t.Fatalf("redacted header count = %d, want 3:\n%s", got, output.String())
	}
}

func TestInspectEmptyHeadersAndBinaryBody(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()
	d.Query = ""
	d.Headers = http.Header{"Content-Type": []string{"application/octet-stream"}}
	d.Body = []byte{0x00, 0x1b, 0xff}

	_, _ = p.Handle(d)

	wantBody := "└─ body        application/octet-stream · 3 B\n\n"
	if !strings.Contains(output.String(), wantBody) {
		t.Fatalf("binary body rendered incorrectly:\n%s", output.String())
	}
	if strings.Contains(output.String(), string(d.Body)) {
		t.Fatalf("binary bytes were rendered:\n%s", output.String())
	}

	output.Reset()
	d.Headers = nil
	d.Body = nil
	_, _ = p.Handle(d)
	if !strings.Contains(output.String(), "├─ headers     (empty)") {
		t.Fatalf("empty headers marker missing:\n%s", output.String())
	}
}

func TestInspectTruncatesHeadersValuesQueryAndBody(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{
		MaxBodyLines:  2,
		MaxHeaders:    1,
		MaxValueChars: 4,
	})
	d := delivery()
	d.Query = "token=abcdefgh&ok=yes"
	d.Headers = http.Header{
		"A-First": []string{"abcdefgh"},
		"B-Next":  []string{"second"},
		"C-Last":  []string{"third"},
	}
	d.Body = []byte("123456\nsecond\nthird")

	_, _ = p.Handle(d)
	got := output.String()
	for _, wanted := range []string{
		"token=abcd… (truncated)&ok=yes",
		"a-first: abcd… (truncated)",
		"… (2 headers omitted)",
		"    1234… (truncated)",
		"    seco… (truncated)",
		"    … (1 lines omitted)",
	} {
		if !strings.Contains(got, wanted) {
			t.Errorf("output missing %q:\n%s", wanted, got)
		}
	}
}

func TestZeroLimitsAreUnlimited(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()
	d.Query = "token=abcdefgh"
	d.Headers = http.Header{"A": []string{"abcdefgh"}, "B": []string{"second"}}
	d.Body = []byte("first\nsecond\nthird")

	_, _ = p.Handle(d)
	if strings.Contains(output.String(), "truncated") || strings.Contains(output.String(), "omitted") {
		t.Fatalf("unlimited output was truncated:\n%s", output.String())
	}
}

func TestInspectEscapesTerminalControlsAndPreservesTabs(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()
	d.Query = ""
	d.Headers = nil
	d.Body = []byte("hello\tworld\x1b[31m")

	_, _ = p.Handle(d)
	if !strings.Contains(output.String(), "hello\tworld\\x1b[31m") {
		t.Fatalf("terminal controls not escaped:\n%q", output.String())
	}
}

func TestForwardSuccessGoldenAndSummaryPriority(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()
	d.Body = []byte(`{"action":"fallback","event_type":"third","event":"second","type":"first"}`)

	p.PrintForward(d, ForwardOutcome{
		Response: ws.Response{Status: http.StatusOK},
		Latency:  38 * time.Millisecond,
	})

	want := fmt.Sprintf(
		"12:34:56.789  ● stripe   POST  /api/webhooks  →  200 OK  38ms  id req_01JFULLREQUESTUID  first · %s\n",
		formatBytes(len(d.Body)),
	)
	if got := output.String(); got != want {
		t.Fatalf("success output:\n%s\nwant:\n%s", got, want)
	}
}

func TestForwardSuccessUsesMIMEFallbackAndCompactLongLatency(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()
	d.Body = []byte(`{"id":"evt_1"}`)

	p.PrintForward(d, ForwardOutcome{
		Response: ws.Response{Status: http.StatusNoContent},
		Latency:  1250 * time.Millisecond,
	})

	if !strings.Contains(output.String(), "→  204 No Content  1.2s") {
		t.Fatalf("status or latency missing:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "application/json · 14 B") {
		t.Fatalf("MIME fallback missing:\n%s", output.String())
	}
}

func TestForwardHTTPFailureGolden(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()
	d.Query = ""
	d.Body = []byte(`{"type":"invoice.payment_failed"}`)
	responseBody := []byte(`{"error":"missing customer_id"}`)

	p.PrintForward(d, ForwardOutcome{
		Response: ws.Response{
			Status:  http.StatusUnprocessableEntity,
			Headers: http.Header{"content-type": []string{"application/json"}},
			Body:    responseBody,
		},
		Latency: 12 * time.Millisecond,
	})

	want := fmt.Sprintf(
		"12:34:56.789  ● stripe   POST  /api/webhooks  →  422 Unprocessable Entity  12ms  id req_01JFULLREQUESTUID\n"+
			"├─ request     application/json · %s\n"+
			"│              {\n"+
			"│                \"type\": \"invoice.payment_failed\"\n"+
			"│              }\n"+
			"│\n"+
			"└─ response    application/json · %s\n"+
			"               {\n"+
			"                 \"error\": \"missing customer_id\"\n"+
			"               }\n\n",
		formatBytes(len(d.Body)),
		formatBytes(len(responseBody)),
	)
	if got := output.String(); got != want {
		t.Fatalf("HTTP failure output:\n%s\nwant:\n%s", got, want)
	}
}

func TestForwardTransportFailureGolden(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{})
	d := delivery()

	p.PrintForward(d, ForwardOutcome{
		Latency: 4 * time.Millisecond,
		Failure: &proxy.TransportFailure{
			Kind: proxy.TransportConnectionRefused,
			Err:  errors.New("dial tcp: connection refused"),
		},
		TargetURL: "http://localhost:3000",
	})

	want := "12:34:56.789  ● stripe   POST  /api/webhooks  →  ✗ connection refused  4ms  id req_01JFULLREQUESTUID\n" +
		"└─ target      http://localhost:3000 is not reachable — is your server running?\n\n"
	if got := output.String(); got != want {
		t.Fatalf("transport output:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(output.String(), "502") {
		t.Fatal("transport failure was displayed as an HTTP 502")
	}
}

func TestForwardReplayTagAndSummaryLimit(t *testing.T) {
	var output bytes.Buffer
	p := newTestPrinter(t, &output, Limits{MaxValueChars: 5})
	d := delivery()

	p.PrintForward(d, ForwardOutcome{
		Response: ws.Response{Status: http.StatusCreated},
		Latency:  100 * time.Microsecond,
		Replay:   true,
	})

	if !strings.Contains(output.String(), "1ms  id req_01JFULLREQUESTUID  replay  payme… (truncated) ·") {
		t.Fatalf("replay tag or summary truncation missing:\n%s", output.String())
	}
}

func TestSourceTokensAlignAndUseStableColor(t *testing.T) {
	oldNoColor, hadNoColor := os.LookupEnv("NO_COLOR")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadNoColor {
			_ = os.Setenv("NO_COLOR", oldNoColor)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})

	var output bytes.Buffer
	p := New(&output, Options{
		Color: true,
		Sources: map[string]string{
			"src_stripe":  "stripe",
			"src_shopify": "shopify",
		},
	})
	fixed(t, p)
	d := delivery()
	d.Headers = nil
	d.Body = nil
	_, _ = p.Handle(d)
	_, _ = p.Handle(d)

	color := fmt.Sprintf("\x1b[%dm● stripe \x1b[0m", sourceColor("src_stripe"))
	if got := strings.Count(output.String(), color); got != 2 {
		t.Fatalf("stable aligned color token count = %d, want 2:\n%q", got, output.String())
	}
}

func TestSourceColorPaletteExcludesFailureColors(t *testing.T) {
	for _, code := range sourceColorCodes {
		if code == 31 || code == 91 {
			t.Fatalf("source palette contains failure color %d", code)
		}
	}
}

func TestForwardStatusesUseSemanticColors(t *testing.T) {
	oldNoColor, hadNoColor := os.LookupEnv("NO_COLOR")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadNoColor {
			_ = os.Setenv("NO_COLOR", oldNoColor)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})

	tests := []struct {
		name       string
		status     int
		failure    *proxy.TransportFailure
		wantStatus string
	}{
		{name: "success", status: http.StatusCreated, wantStatus: "\x1b[32m201 Created\x1b[0m"},
		{name: "redirect", status: http.StatusTemporaryRedirect, wantStatus: "\x1b[33m307 Temporary Redirect\x1b[0m"},
		{name: "HTTP failure", status: http.StatusBadRequest, wantStatus: "\x1b[31m400 Bad Request\x1b[0m"},
		{
			name: "transport failure",
			failure: &proxy.TransportFailure{
				Kind: proxy.TransportConnectionRefused,
				Err:  errors.New("connection refused"),
			},
			wantStatus: "\x1b[31m✗ connection refused\x1b[0m",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			p := New(&output, Options{
				Color:   true,
				Sources: map[string]string{"src_stripe": "stripe"},
			})
			fixed(t, p)
			p.PrintForward(delivery(), ForwardOutcome{
				Response:  ws.Response{Status: test.status},
				Failure:   test.failure,
				TargetURL: "http://localhost:3000",
			})

			if !strings.Contains(output.String(), test.wantStatus) {
				t.Fatalf("output missing semantic status %q:\n%q", test.wantStatus, output.String())
			}
		})
	}
}

func TestNoColorDisablesANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var output bytes.Buffer
	p := New(&output, Options{
		Color:   true,
		Sources: map[string]string{"src_stripe": "stripe"},
	})
	fixed(t, p)
	d := delivery()
	d.Headers = nil
	d.Body = nil
	_, _ = p.Handle(d)

	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("NO_COLOR output contains ANSI escapes: %q", output.String())
	}
}
