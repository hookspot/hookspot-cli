package printer

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"hookspot/internal/ws"
)

func fixed(t *testing.T, p *Printer) {
	t.Helper()
	p.now = func() time.Time {
		return time.Date(2026, 7, 12, 12, 34, 56, 0, time.UTC)
	}
}

func delivery() ws.Delivery {
	return ws.Delivery{
		AttemptUID: "att-1",
		Method:     "POST",
		Path:       "/webhooks/stripe",
		Body:       []byte(`{"id":"evt_1"}`),
	}
}

func TestHandlePrintsAndAcks(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, false)
	fixed(t, p)

	resp, err := p.Handle(delivery())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("ack status = %d, want %d", resp.Status, http.StatusOK)
	}
	want := "12:34:56 --> POST /webhooks/stripe [att-1]\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestHandlePrintsBodyWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, true)
	fixed(t, p)

	if _, err := p.Handle(delivery()); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	want := "12:34:56 --> POST /webhooks/stripe [att-1]\n{\"id\":\"evt_1\"}\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestHandleDefaultsEmptyMethodToPOST(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, false)
	fixed(t, p)

	d := delivery()
	d.Method = ""
	if _, err := p.Handle(d); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	want := "12:34:56 --> POST /webhooks/stripe [att-1]\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestWrapPrintsRequestAndResponse(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, false)
	fixed(t, p)

	next := func(d ws.Delivery) (ws.Response, error) {
		return ws.Response{Status: http.StatusCreated}, nil
	}
	resp, err := p.Wrap(next)(delivery())
	if err != nil {
		t.Fatalf("wrapped handler returned error: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Errorf("status = %d, want %d", resp.Status, http.StatusCreated)
	}
	want := "12:34:56 --> POST /webhooks/stripe [att-1]\n" +
		"12:34:56 <-- [201] POST /webhooks/stripe\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestWrapPassesThroughError(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, false)
	fixed(t, p)

	next := func(d ws.Delivery) (ws.Response, error) {
		return ws.Response{}, http.ErrHandlerTimeout
	}
	if _, err := p.Wrap(next)(delivery()); err != http.ErrHandlerTimeout {
		t.Fatalf("err = %v, want %v", err, http.ErrHandlerTimeout)
	}
	want := "12:34:56 --> POST /webhooks/stripe [att-1]\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q (no <-- line on error)", buf.String(), want)
	}
}
