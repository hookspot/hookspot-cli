package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// TestTerminalCtrlC presses Ctrl-C in raw mode, where it's a key rather than
// a SIGINT. The first press stops listening and exits 0, as a single SIGINT
// does; a second forces the exit with 130. Either way the terminal is
// restored.
func TestTerminalCtrlC(t *testing.T) {
	live := regexp.MustCompile(`(?m)^● live · Acme \| Payments · 0 requests`)
	for _, mode := range []struct {
		name   string
		stream bool
	}{
		{name: "full-screen"},
		{name: "stream", stream: true},
	} {
		start := func(t *testing.T) *terminalRun {
			hookspot := startFakeHookspot(t, fakeHookspotSources)
			args := hookspot.listen()
			if mode.stream {
				args = hookspot.listen("--stream")
			}
			run := startTerminal(t, terminalOptions{width: 100, height: 30}, developmentMetadata(hookspot.url), args...)
			// The first frame comes after raw mode is on; before it, Ctrl-C
			// would be a SIGINT.
			run.waitFor("the live status", live.MatchString)
			return run
		}

		t.Run(mode.name+" once", func(t *testing.T) {
			run := start(t)
			run.send("\x03")
			if code := run.wait(); code != 0 {
				t.Fatalf("exit code after Ctrl-C = %d, want 0; screen:\n%s", code, run.text())
			}
			// The stream's last frame stays, without the prompt.
			if screen := strings.TrimRight(run.text(), "\n"); mode.stream && !strings.HasSuffix(screen, "\n■ stopped · Acme | Payments · 0 requests") {
				t.Errorf("screen after Ctrl-C doesn't end with the stopped status:\n%s", screen)
			}
			if run.altScreen() {
				t.Error("still on the alt screen after Ctrl-C")
			}
			run.requireRestored()
		})

		t.Run(mode.name+" twice", func(t *testing.T) {
			// Stopping takes about 100µs, so even typed together the second
			// press can come after it: that run exits 0, and another tries
			// again.
			for attempt := 1; ; attempt++ {
				run := start(t)
				run.send("\x03\x03")
				code := run.wait()
				if run.altScreen() {
					t.Error("still on the alt screen after two Ctrl-Cs")
				}
				run.requireRestored()
				if code == 130 {
					return
				}
				if code != 0 || attempt == 3 {
					t.Fatalf("exit code after two Ctrl-Cs = %d in run %d, want 130; screen:\n%s", code, attempt, run.text())
				}
			}
		})
	}
}
