package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportFixture(t *testing.T) {
	t.Chdir(t.TempDir())
	fixtures, err := filepath.Abs(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := newTestSession()
	d := delivery("/orders")
	d.Headers["Authorization"] = []string{"Bearer secret"}
	d.Body = []byte{0, 0x1b, 0xff}
	traversal := delivery("/orders")
	traversal.RequestUID = "../../etc/passwd"
	if _, err := s.Handle(d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Handle(traversal); err != nil {
		t.Fatal(err)
	}

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
	for _, test := range []struct {
		name   string
		n      int
		redact bool
		want   Fixture
		json   string
	}{
		{name: "redacted", n: 1, redact: true, want: Fixture{JSON: filepath.Join(fixtures, "req_1.json"), Body: filepath.Join(fixtures, "req_1.body"), Redacted: true}, json: fixtureJSON("[redacted]")},
		// The same request UID replaces the files.
		{name: "shown", n: 1, want: Fixture{JSON: filepath.Join(fixtures, "req_1.json"), Body: filepath.Join(fixtures, "req_1.body")}, json: fixtureJSON("Bearer secret")},
		{name: "unsafe request UID", n: 2, redact: true, want: Fixture{JSON: filepath.Join(fixtures, "entry-2.json"), Body: filepath.Join(fixtures, "entry-2.body")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, err := s.ExportFixture(test.n, test.redact)
			if err != nil || fixture != test.want {
				t.Fatalf("ExportFixture = %+v, %v; want %+v", fixture, err, test.want)
			}
			body, err := os.ReadFile(fixture.Body)
			if err != nil {
				t.Fatal(err)
			}
			entry, _ := s.entry(test.n)
			if string(body) != string(entry.Delivery.Body) {
				t.Errorf("body = %q, want %q", body, entry.Delivery.Body)
			}
			if test.json == "" {
				return
			}
			if json, err := os.ReadFile(fixture.JSON); err != nil || string(json) != test.json {
				t.Errorf("fixture = %s, %v; want:\n%s", json, err, test.json)
			}
		})
	}

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
