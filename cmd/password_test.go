package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

type notifyingWriter struct {
	once  sync.Once
	ready chan struct{}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type readTrackingReader struct{ read bool }

func (r *readTrackingReader) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

func (w *notifyingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.ready) })
	return len(p), nil
}

func TestCredentialReaderAcceptsNewlineAndFinalEOF(t *testing.T) {
	for _, input := range []string{"test-key\n", "test-key"} {
		var prompt bytes.Buffer
		value, err := new(credentialReader).read(context.Background(), strings.NewReader(input), &prompt)
		if err != nil || value != "test-key" {
			t.Fatalf("input %q: value = %q, err = %v", input, value, err)
		}
		if prompt.String() != "Enter your hookspot CLI key: " {
			t.Fatalf("prompt = %q", prompt.String())
		}
	}
}

func TestCredentialReaderCancellationIsBoundedAndOneShot(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := new(credentialReader)
	if _, err := input.read(ctx, reader, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled read error = %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	input = new(credentialReader)
	done := make(chan error, 1)
	prompt := &notifyingWriter{ready: make(chan struct{})}
	go func() { _, err := input.read(ctx, reader, prompt); done <- err }()
	<-prompt.ready
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read error = %v", err)
	}
	if _, err := input.read(context.Background(), strings.NewReader("another\n"), io.Discard); err == nil {
		t.Fatal("second read was accepted")
	}
}

func TestCredentialReaderRejectsOversizedInputWithoutEchoingIt(t *testing.T) {
	input := strings.Repeat("x", maxLoginKeyBytes+1)
	var output bytes.Buffer
	value, err := new(credentialReader).read(context.Background(), strings.NewReader(input), &output)
	if err == nil || value != "" {
		t.Fatalf("value length = %d, err = %v", len(value), err)
	}
	if strings.Contains(output.String(), input) || strings.Contains(err.Error(), input) {
		t.Fatal("oversized input was exposed")
	}
}

func TestCredentialReaderDoesNotReadWhenPromptFails(t *testing.T) {
	input := new(readTrackingReader)
	wantErr := errors.New("output unavailable")
	value, err := new(credentialReader).read(context.Background(), input, failingWriter{err: wantErr})
	if !errors.Is(err, wantErr) || value != "" {
		t.Fatalf("value = %q, err = %v", value, err)
	}
	if input.read {
		t.Fatal("input read started after prompt failure")
	}
}
