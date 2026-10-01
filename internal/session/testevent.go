package session

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"hookspot/internal/api"
	"hookspot/internal/ws"
)

// testHeader carries a test event's id through Hookspot to its deliveries.
const testHeader = "X-Hookspot-Test"

// ErrNotListening refuses a test event to a source this run doesn't listen to.
var ErrNotListening = errors.New("not a source this run listens to")

var testClient = &http.Client{
	Timeout: 10 * time.Second,
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
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		status := strconv.Itoa(response.StatusCode)
		if text := http.StatusText(response.StatusCode); text != "" {
			status += " " + text
		}
		return "", fmt.Errorf("test event to %s: Hookspot answered %s", source.Name, status)
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
	return api.Source{}, fmt.Errorf("%s: %w", name, ErrNotListening)
}

// sentTest reports whether delivery carries the id of a test event this run
// sent. Senders and Hookspot may change the header name's case.
func (s *Session) sentTest(delivery ws.Delivery) bool {
	for key, values := range delivery.Headers {
		if strings.EqualFold(key, testHeader) && len(values) > 0 {
			s.mu.Lock()
			defer s.mu.Unlock()
			return slices.Contains(s.tests, values[0])
		}
	}
	return false
}

// TestCurl is a POSIX shell command that sends a test event to publicURL from
// anywhere. It carries no test id, so its deliveries aren't marked Test.
func TestCurl(publicURL string) string {
	return "curl -X POST '" + strings.ReplaceAll(publicURL, "'", `'\''`) + `' -H 'Content-Type: application/json' -d '{"type":"hookspot.test"}'`
}
