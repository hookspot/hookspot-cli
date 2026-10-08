package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hookspot/internal/ws"
)

func TestExportFixture(t *testing.T) {
	t.Chdir(t.TempDir())
	fixtures, err := filepath.Abs(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := newTestSession()
	d := delivery("rte_orders", "/orders")
	d.Headers["Authorization"] = []string{"Bearer secret"}
	d.Body = []byte{0, 0x1b, 0xff}
	traversal := delivery("rte_orders", "/orders")
	traversal.RequestUID = "../../etc/passwd"
	noRequest := delivery("rte_orders", "/orders")
	noRequest.RequestUID = ""
	newRoute := delivery("", "/unmatched")
	newRoute.RouteUID = "rte_new"
	// Hookspot delivers one request to each route of its source.
	for _, sent := range []ws.Delivery{d, delivery("rte_refunds", "/refunds"), delivery("", "/unmatched"), traversal, noRequest, newRoute} {
		if _, err := s.Handle(sent); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Replay(1); err != nil {
		t.Fatal(err)
	}

	t.Run("shown over redacted", func(t *testing.T) {
		fixtureJSON := func(authorization string) string {
			return `{
  "method": "PUT",
  "path": "/orders",
  "query": "a=1",
  "headers": {
    "Authorization": [
      "` + authorization + `"
    ],
    "X-Test": [
      "original"
    ]
  }
}
`
		}
		want := Fixture{JSON: filepath.Join(fixtures, "req_1_rte_orders.json"), Body: filepath.Join(fixtures, "req_1_rte_orders.body")}
		for _, test := range []struct {
			redact bool
			json   string
		}{
			{redact: true, json: fixtureJSON("[redacted]")},
			// Exporting the same request again replaces its files.
			{json: fixtureJSON("Bearer secret")},
		} {
			fixture, err := s.ExportFixture(1, test.redact)
			if want.Redacted = test.redact; err != nil || fixture != want {
				t.Fatalf("ExportFixture(redact %v) = %+v, %v; want %+v", test.redact, fixture, err, want)
			}
			if json, err := os.ReadFile(fixture.JSON); err != nil || string(json) != test.json {
				t.Errorf("fixture = %s, %v; want:\n%s", json, err, test.json)
			}
			if body, err := os.ReadFile(fixture.Body); err != nil || string(body) != string(d.Body) {
				t.Errorf("body = %q, %v; want %q", body, err, d.Body)
			}
		}
	})

	t.Run("names", func(t *testing.T) {
		for n, want := range map[int]string{
			2: "req_1_rte_refunds",
			3: "req_1",
			4: "entry-4",
			5: "entry-5",
			// A route the session doesn't know yet still tells it apart.
			6: "req_1_rte_new",
			// A replay replaces its original's files.
			7: "req_1_rte_orders",
		} {
			fixture, err := s.ExportFixture(n, true)
			if err != nil || fixture.JSON != filepath.Join(fixtures, want+".json") || fixture.Body != filepath.Join(fixtures, want+".body") {
				t.Errorf("ExportFixture(%d) = %+v, %v; want %s", n, fixture, err, want)
			}
		}
	})

	t.Run("unwritable directory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if err := os.WriteFile(fixtureDir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ExportFixture(1, true); err == nil || !strings.Contains(err.Error(), fixtureDir) {
			t.Errorf("ExportFixture = %v, want the directory's error", err)
		}
		// A binary body needs its .body file.
		if _, err := s.Curl(1, true); err == nil || !strings.Contains(err.Error(), fixtureDir) {
			t.Errorf("Curl = %v, want the directory's error", err)
		}
	})
}
