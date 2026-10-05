package cmd

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// TestTerminalUpdateAlert runs a release older than GitHub's latest: each
// view keeps the alert, with the upgrade command, at the right end of its
// bottom line or after Ready, the join's notice takes its place, and only a
// terminal on stdout asks GitHub.
func TestTerminalUpdateAlert(t *testing.T) {
	var asked atomic.Int32
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0"}`))
	}))
	defer github.Close()
	alert := regexp.QuoteMeta("↑ 1.3.0 · " + upgradeDocsURL)
	release := func(hookspot *fakeHookspot) map[string]string {
		metadata := releaseMetadata("1.2.3")
		metadata["server_url"] = hookspot.url
		return metadata
	}
	options := func(environment map[string]string) terminalOptions {
		environment["TEST_GITHUB_API_URL"] = github.URL
		return terminalOptions{width: 100, height: 30, environment: environment}
	}

	t.Run("full screen", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, options(map[string]string{}), release(hookspot), hookspot.listen()...)
		keys := regexp.MustCompile(`(?m)^\? help  q quit .*…  +` + alert + `$`)
		run.waitFor("the alert after the keys", keys.MatchString)
		run.send("s")
		sourcesKeys := regexp.MustCompile(`(?m)^\? help  q quit  ↑↓ select .*  +` + alert + `$`)
		run.waitFor("the alert on the Sources page", sourcesKeys.MatchString)
	})

	t.Run("notice", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		hookspot.joinReplies <- deprecatedReply
		run := startTerminal(t, options(map[string]string{}), release(hookspot), hookspot.listen()...)
		keys := regexp.MustCompile(`(?m)^\? help  q quit  +` + regexp.QuoteMeta("⚠ "+deprecation) + `$`)
		run.waitFor("the notice after the keys", keys.MatchString)
	})

	t.Run("stream", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, options(map[string]string{}), release(hookspot), hookspot.listen("--stream")...)
		status := regexp.MustCompile(`(?m)^● live · Acme \| Payments · 0 requests +` + alert + `$`)
		run.waitFor("the alert in the status line's hint", status.MatchString)
	})

	t.Run("TERM=dumb", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("ConPTY sends conhost's own rendering of the console, which always has escape sequences")
		}
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, options(map[string]string{"TERM": "dumb"}), release(hookspot), hookspot.listen()...)
		afterReady := regexp.MustCompile(`(?ms)^Ready\. Waiting for requests .*^` + alert + `$`)
		run.waitFor("the alert after Ready", afterReady.MatchString)
	})

	t.Run("stdout piped", func(t *testing.T) {
		before := asked.Load()
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		piped := options(map[string]string{})
		piped.pipeStdout = true
		run := startTerminal(t, piped, release(hookspot), hookspot.listen()...)
		hookspot.deliver(t, terminalDelivery)
		run.waitFor("#1 on stdout", func(string) bool { return strings.Contains(run.piped(), "╭─ #1 stripe · POST /webhooks/stripe ") })
		hookspot.end(t)
		run.wait()
		if asked.Load() != before {
			t.Errorf("piped listen asked GitHub %d times", asked.Load()-before)
		}
	})
}
