package cmd

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"hookspot/internal/ws"
)

// TestTerminalResize covers a resize reflowing each view, down to a
// 60-column terminal that stays usable.
func TestTerminalResize(t *testing.T) {
	t.Run("full screen", func(t *testing.T) {
		local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer local.Close()
		hookspot := startFakeHookspot(t, listenStreamSources)
		run := startTerminal(t, terminalOptions{width: 150, height: 30}, developmentMetadata(hookspot.url), hookspot.listen("--forward-to", local.URL)...)
		hookspot.deliver(t, terminalDelivery)

		split := regexp.MustCompile(`(?m)^╭─ Requests .*╮ ╭─ #1 stripe · POST /webhooks/stripe .*╮$`)
		run.waitFor("the detail beside the list", func(screen string) bool {
			return split.MatchString(screen) && wholeBoxes(screen, 150)
		})

		run.resize(60, 24)
		// The list drops its TIME and METHOD columns and the detail goes below it.
		row := regexp.MustCompile(`(?m)^│ › 1  stripe  /webhooks/stripe +200 +\S+ +│$`)
		detail := regexp.MustCompile(`(?m)^╭─ #1 stripe · POST /webhooks/stripe .*╮$`)
		keys := regexp.MustCompile(`(?m)^\? help  q quit  ↑↓ select  .*…$`)
		run.waitFor("the list above the detail in 60 columns", func(screen string) bool {
			return row.MatchString(screen) && detail.MatchString(screen) && keys.MatchString(screen) && wholeBoxes(screen, 60)
		})

		run.send("\x1b[C")
		run.waitFor("the request tab in 60 columns", func(screen string) bool {
			return strings.Contains(screen, "content-type  application/json") && strings.Contains(screen, `"type": "payment_intent.succeeded"`) && wholeBoxes(screen, 60)
		})

		run.send("s")
		sources := regexp.MustCompile(`(?m)^╭─ Sources & routes .*╮$`)
		route := regexp.MustCompile(`(?m)^│ › stripe  payments `)
		run.waitFor("the Sources page in 60 columns", func(screen string) bool {
			return sources.MatchString(screen) && route.MatchString(screen) && wholeBoxes(screen, 60)
		})
	})

	t.Run("stream", func(t *testing.T) {
		hookspot := startFakeHookspot(t, listenStreamSources)
		// Without a terminal on stdin the status line ends with its hint.
		run := startTerminal(t, terminalOptions{width: 100, height: 40, stdin: strings.NewReader("")}, developmentMetadata(hookspot.url), hookspot.listen()...)
		status := func(requests string, width int) func(string) bool {
			line := regexp.MustCompile(`(?m)^● live · Acme \| Payments · ` + requests + ` +ctrl-c quit$`)
			return func(screen string) bool { return ansi.StringWidth(line.FindString(screen)) == width }
		}
		run.waitFor("the status line in 100 columns", status("0 requests", 100))

		run.resize(60, 40)
		run.waitFor("the status line in 60 columns", status("0 requests", 60))

		hookspot.deliver(t, terminalDelivery)
		counted := status("1 request", 60)
		run.waitFor("card #1 in 60 columns", func(screen string) bool {
			return counted(screen) && wholeCard(screen, 60)
		})
	})
}

// TestTerminalHostileDelivery covers delivery data that would drive the
// terminal: every screen shows it escaped and keeps its boxes whole.
func TestTerminalHostileDelivery(t *testing.T) {
	const hostile = "\x1b]0;pwned\x07\x1b[?1049l\u009b31m\r\n\t\x00\x7f\b界🙂e\u0301"
	// escaped is how hostile starts on screen.
	const escaped = `\x1b]0;pwned\x07\x1b[?1049l\x9b31m`
	delivery := ws.Delivery{
		AttemptUID: "att_1", RequestUID: "req_" + hostile, SourceUID: "src_stripe", Method: "POST", Path: "/webhooks/" + hostile, Query: "q=" + hostile,
		Headers: http.Header{"Content-Type": []string{"text/plain"}, "X-" + hostile: []string{hostile}},
		Body:    []byte("body " + hostile + "\nline 2 " + hostile),
	}
	// requireNoneRaw fails if the terminal got any of hostile's controls as
	// they came. BS is left out: the renderer may move the cursor with it.
	requireNoneRaw := func(t *testing.T, run *terminalRun) {
		t.Helper()
		for _, raw := range []string{"\x1b]0;pwned\x07", "\x1b[?1049l", "\u009b31m", "\x00", "\x7f"} {
			if strings.Contains(run.written(), raw) {
				t.Errorf("the delivery's %q reached the terminal", raw)
			}
		}
	}

	t.Run("full screen", func(t *testing.T) {
		hookspot := startFakeHookspot(t, listenStreamSources)
		run := startTerminal(t, terminalOptions{width: 80, height: 40}, developmentMetadata(hookspot.url), hookspot.listen()...)
		hookspot.deliver(t, delivery)
		run.waitFor("the request and its overview", func(screen string) bool {
			return strings.Contains(screen, "│ › 1  ") && strings.Contains(screen, "╭─ #1 stripe · POST /webhooks/"+escaped) && wholeBoxes(screen, 80)
		})
		run.send("\x1b[C")
		run.waitFor("the request tab", func(screen string) bool {
			return strings.Contains(screen, "│ x-"+escaped) && strings.Contains(screen, "│ body "+escaped) && wholeBoxes(screen, 80)
		})
		run.requireAltScreen()
		requireNoneRaw(t, run)
	})

	t.Run("stream", func(t *testing.T) {
		hookspot := startFakeHookspot(t, listenStreamSources)
		run := startTerminal(t, terminalOptions{width: 80, height: 40}, developmentMetadata(hookspot.url), hookspot.listen("--stream")...)
		hookspot.deliver(t, delivery)
		run.waitFor("the request's card", func(screen string) bool {
			return strings.Contains(screen, "│ body "+escaped) && wholeCard(screen, 80)
		})
		requireNoneRaw(t, run)
	})
}

// boxEdges pairs each box-drawing character that starts a line with the one
// that must end it.
var boxEdges = map[rune]rune{'╭': '╮', '│': '│', '├': '┤', '╰': '╯'}

// wholeBoxes reports whether every line of screen that starts a box ends it
// in column width: nothing in a box wrapped, spilled over or was cut.
func wholeBoxes(screen string, width int) bool {
	for _, line := range strings.Split(screen, "\n") {
		first, _ := utf8.DecodeRuneInString(line)
		last, _ := utf8.DecodeLastRuneInString(line)
		if end, box := boxEdges[first]; box && (last != end || ansi.StringWidth(line) != width) {
			return false
		}
	}
	return true
}

// firstCard is the stream's card #1, from its top border to its bottom one.
var firstCard = regexp.MustCompile(`(?ms)^╭─ #1 .*?^╰[^\n]*`)

// wholeCard reports whether the stream shows card #1 whole in a terminal
// width columns wide. Cards leave the last column free.
func wholeCard(screen string, width int) bool {
	card := firstCard.FindString(screen)
	return card != "" && wholeBoxes(card, width-1)
}
