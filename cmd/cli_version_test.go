package cmd

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// outdatedMessage is the server's refusal of a release below its minimum CLI
// version.
const outdatedMessage = "Hookspot CLI 0.9.0 is too old: upgrade to 1.0.0 or later."

// outdatedReply refuses a join with outdatedMessage.
var outdatedReply = map[string]any{"status": "error", "response": map[string]string{"reason": "cli_outdated", "message": outdatedMessage}}

// TestListenNamesItsReleaseInTheUserAgent covers the API calls and the
// websocket handshake of a release and of a dev build.
func TestListenNamesItsReleaseInTheUserAgent(t *testing.T) {
	for name, metadata := range map[string]map[string]string{"release": releaseMetadata("1.2.3"), "dev": developmentMetadata("")} {
		t.Run(name, func(t *testing.T) {
			hookspot := startFakeHookspot(t, fakeHookspotSources)
			hookspot.joinReplies <- notFoundReply
			metadata["server_url"] = hookspot.url
			runCommandProcess(t, "", metadata, hookspot.listen()...)

			want := "hookspot-cli/" + metadata["version"] + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
			// The project, its sources, then the websocket.
			if got := len(hookspot.userAgents); got != 3 {
				t.Fatalf("requests = %d, want 3", got)
			}
			for range 3 {
				if got := <-hookspot.userAgents; got != want {
					t.Errorf("User-Agent = %q, want %q", got, want)
				}
			}
		})
	}
}

// TestListenStopsWhenRefusedAsOutdated covers a release below the minimum
// CLI version, refused by the API, to listen or login, or by the first join
// or a rejoin: the command prints the server's message and the upgrade
// command, and exits without retrying.
func TestListenStopsWhenRefusedAsOutdated(t *testing.T) {
	refused := outdatedMessage + "\n\n" + upgradeDocsURL + "\n"

	t.Run("API", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusUpgradeRequired)
			_, _ = w.Write([]byte(`{"reason":"cli_outdated","message":"` + outdatedMessage + `"}`))
		}))
		defer server.Close()
		config := filepath.Join(t.TempDir(), "config.toml")
		if err := writeCommandFixture(config, []byte(fakeHookspotConfig)); err != nil {
			t.Fatal(err)
		}
		result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", config, "listen")
		if result.err == nil || result.stderr != refused || requests.Load() != 1 {
			t.Fatalf("listen = %v after %d requests, stderr %q, want %q", result.err, requests.Load(), result.stderr, refused)
		}
	})

	t.Run("login", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUpgradeRequired)
			_, _ = w.Write([]byte(`{"reason":"cli_outdated","message":"` + outdatedMessage + `"}`))
		}))
		defer server.Close()
		result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", filepath.Join(t.TempDir(), "config.toml"), "login")
		if result.err == nil || result.stderr != refused {
			t.Fatalf("login = %v, stderr %q, want %q", result.err, result.stderr, refused)
		}
	})

	t.Run("first join", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		hookspot.joinReplies <- outdatedReply
		result := runCommandProcess(t, "", developmentMetadata(hookspot.url), hookspot.listen()...)
		if result.err == nil || result.stderr != refused || len(hookspot.joins) != 1 {
			t.Fatalf("listen = %v after %d joins, stderr %q", result.err, len(hookspot.joins), result.stderr)
		}
	})

	t.Run("rejoin", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		hookspot.joinReplies <- acceptedReply
		hookspot.joinReplies <- outdatedReply
		hookspot.play(hangUp{})
		result := runCommandProcess(t, "", developmentMetadata(hookspot.url), hookspot.listen()...)
		if result.err == nil || !strings.HasSuffix(result.stderr, "\n"+refused) || len(hookspot.joins) != 2 {
			t.Fatalf("listen = %v after %d joins, stderr %q", result.err, len(hookspot.joins), result.stderr)
		}
		if got := strings.Count(result.stderr, "connection lost"); got != 1 {
			t.Fatalf("reconnect notices = %d, want 1:\n%s", got, result.stderr)
		}
	})
}

// TestListenPrintsTheJoinsNoticeOnce covers plain mode over four joins:
// the first join's notice prints, the second's same one doesn't, and after a
// join without one, the fourth's prints again.
func TestListenPrintsTheJoinsNoticeOnce(t *testing.T) {
	hookspot := startFakeHookspot(t, fakeHookspotSources)
	for _, reply := range []map[string]any{deprecatedReply, deprecatedReply, acceptedReply, deprecatedReply} {
		hookspot.joinReplies <- reply
	}
	hookspot.play(hangUp{}, hangUp{}, hangUp{})
	result := runCommandProcess(t, "", developmentMetadata(hookspot.url), hookspot.listen()...)
	if got := strings.Count(result.stderr, "⚠ "+deprecation+"\n"); got != 2 || strings.Count(result.stderr, "Reconnected after") != 3 {
		t.Fatalf("notices = %d, want 2 over 4 joins:\n%s", got, result.stderr)
	}
}
