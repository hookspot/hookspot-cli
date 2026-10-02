package session

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"hookspot/internal/api"
	"hookspot/internal/ws"
)

const (
	// testHeader carries a test event's id through Hookspot to its deliveries.
	testHeader  = "X-Hookspot-Test"
	testTimeout = 10 * time.Second
	// maxTestDrainBytes caps how much of Hookspot's answer is drained, which
	// lets its connection be reused.
	maxTestDrainBytes = 64 * 1024
)

var testClient = &http.Client{
	Timeout: testTimeout,
	// A redirect means the public URL is wrong, so it's reported like any non-2xx.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// TestHint follows Ready while no request has arrived: the sources a test
// event can check end to end.
type TestHint struct {
	Sources []api.Source
}

func (TestHint) event() {}

// SendTest posts a test event to the named source's public URL, so it takes
// the whole path through Hookspot and shows in the dashboard like any request.
// The name may be empty when this run listens to one source. Every delivery
// carrying the event's id is marked Test; a source with several routes makes
// several. It returns the source's name.
func (s *Session) SendTest(name string) (string, error) {
	source, err := s.testSource(name)
	if err != nil {
		return "", err
	}
	id := rand.Text()
	// Hookspot may deliver the event before it answers the POST.
	s.mu.Lock()
	s.tests = append(s.tests, id)
	s.mu.Unlock()

	body, err := json.Marshal(struct {
		Type   string    `json:"type"`
		SentAt time.Time `json:"sent_at"`
	}{"hookspot.test", s.now().UTC()})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(s.ctx, http.MethodPost, source.URL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("test event to %s: %w", source.Name, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(testHeader, id)
	response, err := testClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("test event to %s: %w", source.Name, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxTestDrainBytes))
	_ = response.Body.Close()
	if !Success(response.StatusCode) {
		return "", fmt.Errorf("test event to %s: Hookspot answered %s", source.Name, StatusText(response.StatusCode))
	}
	return source.Name, nil
}

// testSource is the listened source called name, or the only one when name is
// empty.
func (s *Session) testSource(name string) (api.Source, error) {
	if name == "" {
		if len(s.sources) == 1 {
			return s.sources[0], nil
		}
		commands := make([]string, len(s.sources))
		for i, source := range s.sources {
			commands[i] = "t " + source.Name
		}
		return api.Source{}, fmt.Errorf("test which source? %s", strings.Join(commands, " · "))
	}
	for _, source := range s.sources {
		if source.Name == name {
			return source, nil
		}
	}
	return api.Source{}, fmt.Errorf("%s: not a source this run listens to", name)
}

// sentTest reports whether delivery carries the id of a test event this run
// sent.
func (s *Session) sentTest(delivery ws.Delivery) bool {
	id := HeaderValue(delivery.Headers, testHeader)
	s.mu.Lock()
	defer s.mu.Unlock()
	return id != "" && slices.Contains(s.tests, id)
}

// TestCurl is a POSIX shell command that sends a test event to publicURL from
// anywhere. It carries no test id, so its deliveries aren't marked Test.
func TestCurl(publicURL string) string {
	return "curl -X POST " + Quote(publicURL) + ` -H 'Content-Type: application/json' -d '{"type":"hookspot.test"}'`
}
