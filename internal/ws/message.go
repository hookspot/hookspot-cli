package ws

import (
	"encoding/json"
	"fmt"
)

// message is a Phoenix Channels V2 frame:
// [join_ref, ref, topic, event, payload].
// JoinRef and Ref are nil for server pushes and serialize as JSON null.
type message struct {
	JoinRef *string
	Ref     *string
	Topic   string
	Event   string
	Payload json.RawMessage
}

// encode marshals m to the V2 array frame. A nil Payload becomes "{}".
func encode(m message) ([]byte, error) {
	payload := m.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	frame := []interface{}{m.JoinRef, m.Ref, m.Topic, m.Event, payload}
	return json.Marshal(frame)
}

// decode parses a V2 array frame into a message.
func decode(data []byte) (message, error) {
	var frame []json.RawMessage
	if err := json.Unmarshal(data, &frame); err != nil {
		return message{}, fmt.Errorf("decode frame: %w", err)
	}
	if len(frame) != 5 {
		return message{}, fmt.Errorf("decode frame: got %d elements, want 5", len(frame))
	}

	var m message
	if err := json.Unmarshal(frame[0], &m.JoinRef); err != nil {
		return message{}, fmt.Errorf("decode join_ref: %w", err)
	}
	if err := json.Unmarshal(frame[1], &m.Ref); err != nil {
		return message{}, fmt.Errorf("decode ref: %w", err)
	}
	if err := json.Unmarshal(frame[2], &m.Topic); err != nil {
		return message{}, fmt.Errorf("decode topic: %w", err)
	}
	if err := json.Unmarshal(frame[3], &m.Event); err != nil {
		return message{}, fmt.Errorf("decode event: %w", err)
	}
	m.Payload = frame[4]
	return m, nil
}
