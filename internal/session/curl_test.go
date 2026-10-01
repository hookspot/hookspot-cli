package session

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"

	"hookspot/internal/api"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

// received is a request the curl command made.
type received struct {
	method, path, query string
	header              http.Header
	body                []byte
}

func TestCurlReproducesTheRequest(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}
	t.Chdir(t.TempDir())
	fixtures, err := filepath.Abs(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan received, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Forwarding reaches this server too; only curl's requests count.
		if !strings.HasPrefix(r.UserAgent(), "curl/") {
			return
		}
		body, _ := io.ReadAll(r.Body)
		requests <- received{r.Method, r.URL.Path, r.URL.RawQuery, r.Header, body}
	}))
	t.Cleanup(server.Close)

	text := ws.Delivery{
		AttemptUID: "att_1", RequestUID: "req_text", SourceUID: "src_stripe", Method: http.MethodPatch, Path: "/orders", Query: "a=1&b=it%27s",
		Headers: http.Header{
			"Content-Type":   []string{"application/json"},
			"Authorization":  []string{"Bearer secret"},
			"X-Quote":        []string{`it's "quoted"`},
			"X-Empty":        []string{""},
			"X-Many":         []string{"one", "two"},
			"Connection":     []string{"keep-alive"},
			"Host":           []string{"hookspot.test"},
			"Content-Length": []string{"999"},
		},
		Body: []byte("{\"note\":\"it's\n\t\\\"quoted\\\"\"}"),
	}
	binary := ws.Delivery{
		AttemptUID: "att_2", RequestUID: "req_binary", SourceUID: "src_stripe", Method: http.MethodPost, Path: "/refunds",
		Headers: http.Header{"X-Signature": []string{"sig\u009bvalue"}, "X-Tab": []string{"a\tb"}, "X-Plain": []string{"plain"}},
		Body:    []byte{0, 0x1b, '[', '2', 'J', 0xff},
	}
	// curl reads a body starting with @ as a file name.
	at := ws.Delivery{AttemptUID: "att_3", RequestUID: "req_at", SourceUID: "src_stripe", Method: http.MethodPost, Path: "/orders", Body: []byte("@/etc/hostname")}

	for _, mode := range []struct {
		name    string
		forward bool
		path    func(ws.Delivery) string
	}{
		{name: "forward", forward: true, path: func(d ws.Delivery) string { return d.Path }},
		{name: "inspect", path: func(ws.Delivery) string { return "/in/src_stripe" }},
	} {
		t.Run(mode.name, func(t *testing.T) {
			sources := []api.Source{{UID: "src_stripe", URL: server.URL + "/in/src_stripe", Routes: testSources[0].Routes}}
			var forwarder Forwarder
			if mode.forward {
				local, err := proxy.New(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				forwarder = local
			}
			s := New(context.Background(), sources, forwarder, &recorder{})
			for _, d := range []ws.Delivery{text, binary, at} {
				if _, err := s.Handle(d); err != nil {
					t.Fatal(err)
				}
			}

			for n, test := range []struct {
				delivery    ws.Delivery
				inlineBody  bool
				headersFile bool
			}{
				{delivery: text, inlineBody: true},
				{delivery: binary, headersFile: true},
				{delivery: at},
			} {
				d := test.delivery
				curl, err := s.Curl(n+1, true)
				if err != nil {
					t.Fatal(err)
				}
				if curl.Resend == mode.forward {
					t.Errorf("#%d Resend = %v in %s mode", n+1, curl.Resend, mode.name)
				}
				if inline := strings.Contains(curl.Command, "--data-binary '"+strings.ReplaceAll(string(d.Body), "'", `'\''`)+"'"); inline != test.inlineBody {
					t.Errorf("#%d body inline = %v:\n%s", n+1, inline, curl.Command)
				}
				if !test.inlineBody && !strings.Contains(curl.Command, "--data-binary '@"+filepath.Join(fixtures, d.RequestUID+".body")+"'") {
					t.Errorf("#%d body is not read from its absolute fixture path:\n%s", n+1, curl.Command)
				}
				if test.headersFile != (curl.HeadersFile != "") || test.headersFile && curl.HeadersFile != filepath.Join(fixtures, d.RequestUID+".headers") {
					t.Errorf("#%d HeadersFile = %q", n+1, curl.HeadersFile)
				}
				if !test.inlineBody && strings.ContainsFunc(curl.Command, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) {
					t.Errorf("#%d command has a raw control character: %q", n+1, curl.Command)
				}

				run := exec.Command("sh", "-c", curl.Command)
				// The pasted command works from any directory.
				run.Dir = t.TempDir()
				if out, err := run.CombinedOutput(); err != nil {
					t.Fatalf("#%d curl: %v\n%s", n+1, err, out)
				}
				got := <-requests
				if got.method != d.Method || got.path != mode.path(d) || got.query != d.Query || string(got.body) != string(d.Body) {
					t.Errorf("#%d curl sent %s %s?%s %q, want %s %s?%s %q", n+1, got.method, got.path, got.query, got.body, d.Method, mode.path(d), d.Query, d.Body)
				}
				for name, values := range d.Headers {
					if !slices.Contains([]string{"Connection", "Host", "Content-Length"}, name) && !slices.Equal(got.header.Values(name), values) {
						t.Errorf("#%d header %s = %q, want %q", n+1, name, got.header.Values(name), values)
					}
				}
				if got.header.Get("Connection") == "keep-alive" || got.header.Get("Content-Length") == "999" {
					t.Errorf("#%d curl sent a hop-by-hop header or the delivery's Content-Length: %v", n+1, got.header)
				}
			}
		})
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(fixtures, "req_binary.headers"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("headers file = %v, %v; want mode 0600", info, err)
		}
	}
}

func TestCurlRedactsOnlyTheShownCommand(t *testing.T) {
	s, _, _ := newTestSession()
	d := delivery("/orders")
	d.Headers["Authorization"] = []string{"Bearer secret"}
	if _, err := s.Handle(d); err != nil {
		t.Fatal(err)
	}

	redacted, err := s.Curl(1, true)
	if err != nil {
		t.Fatal(err)
	}
	if !redacted.Redacted || !strings.Contains(redacted.Shown, "-H 'Authorization: [redacted]'") || strings.Contains(redacted.Shown, "secret") {
		t.Errorf("shown command:\n%s", redacted.Shown)
	}
	if !strings.Contains(redacted.Command, "-H 'Authorization: Bearer secret'") {
		t.Errorf("full command:\n%s", redacted.Command)
	}

	shown, err := s.Curl(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if shown.Redacted || shown.Shown != shown.Command || shown.Command != redacted.Command {
		t.Errorf("with --show-sensitive-headers, shown:\n%s\nfull:\n%s", shown.Shown, shown.Command)
	}
}

func TestCurlRefusesControlCharactersInTheURL(t *testing.T) {
	s, _, _ := newTestSession()
	d := delivery("/orders")
	d.Query = "a=\x1b[2J"
	if _, err := s.Handle(d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Curl(1, true); err == nil || err.Error() != "#1: its method or URL has control characters" {
		t.Fatalf("Curl = %v", err)
	}
}
