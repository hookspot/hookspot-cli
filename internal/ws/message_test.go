package ws

import (
	"encoding/json"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestEncode_ClientFrameWithStringRefs(t *testing.T) {
	m := message{
		JoinRef: strPtr("1"),
		Ref:     strPtr("1"),
		Topic:   "project:proj_1",
		Event:   "phx_join",
		Payload: json.RawMessage(`{"sources":["stripe"]}`),
	}

	data, err := encode(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := `["1","1","project:proj_1","phx_join",{"sources":["stripe"]}]`
	if string(data) != want {
		t.Fatalf("encode = %s, want %s", data, want)
	}
}

func TestEncode_NilRefsAndPayload(t *testing.T) {
	m := message{Topic: "phoenix", Event: "heartbeat"}

	data, err := encode(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := `[null,null,"phoenix","heartbeat",{}]`
	if string(data) != want {
		t.Fatalf("encode = %s, want %s", data, want)
	}
}

func TestDecode_ServerPushWithNullRefs(t *testing.T) {
	data := []byte(`[null,null,"project:proj_1","delivery_attempt.created",{"id":"da_1"}]`)

	m, err := decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if m.JoinRef != nil || m.Ref != nil {
		t.Fatalf("refs = %v/%v, want nil/nil", m.JoinRef, m.Ref)
	}
	if m.Topic != "project:proj_1" {
		t.Fatalf("topic = %q, want %q", m.Topic, "project:proj_1")
	}
	if m.Event != "delivery_attempt.created" {
		t.Fatalf("event = %q, want %q", m.Event, "delivery_attempt.created")
	}
	if string(m.Payload) != `{"id":"da_1"}` {
		t.Fatalf("payload = %s, want %s", m.Payload, `{"id":"da_1"}`)
	}
}

func TestDecode_ReplyWithStringRef(t *testing.T) {
	data := []byte(`["1","1","project:proj_1","phx_reply",{"status":"ok","response":{}}]`)

	m, err := decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if m.Ref == nil || *m.Ref != "1" {
		t.Fatalf("ref = %v, want \"1\"", m.Ref)
	}
	if m.Event != "phx_reply" {
		t.Fatalf("event = %q, want phx_reply", m.Event)
	}
}
