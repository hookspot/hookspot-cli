package tui

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"hookspot/internal/cards"
	"hookspot/internal/session"
)

var (
	filterKey = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	// escKey stops w's wait, else dismisses the alerts, else clears the
	// filter.
	escKey = key.NewBinding(key.WithKeys("esc"))
)

// filter narrows the list to the requests matching every term of its text.
// It's typed on the filter line and applied with enter.
type filter struct {
	// editing is set while the filter line takes keys; input is what's typed.
	editing bool
	input   string
	// text is the applied filter, "" for none; numbers are the requests it
	// shows, oldest first, matched once since a summary decodes the body.
	text    string
	numbers []int
}

// term is one word of a filter: status:, source: or path: with a value, or
// free text.
type term struct {
	key, value string
}

// parseFilter splits text into terms; a word with any other key, or none
// after the colon, is free text.
func parseFilter(text string) []term {
	var terms []term
	for _, word := range strings.Fields(text) {
		key, value, found := strings.Cut(word, ":")
		if found && value != "" && (key == "status" || key == "source" || key == "path") {
			terms = append(terms, term{key: key, value: value})
		} else {
			terms = append(terms, term{value: word})
		}
	}
	return terms
}

// matches reports whether e matches every term.
func matches(e session.Entry, terms []term, l cards.Listen) bool {
	for _, t := range terms {
		if !t.matches(e, l) {
			return false
		}
	}
	return true
}

// matches compares sources ignoring case and paths by prefix; free text is
// looked for in the path and the summary, ignoring case.
func (t term) matches(e session.Entry, l cards.Listen) bool {
	switch t.key {
	case "status":
		return statusMatches(e, t.value)
	case "source":
		return strings.EqualFold(l.Sources[e.Delivery.SourceUID], t.value)
	case "path":
		return strings.HasPrefix(e.Delivery.Path, t.value)
	}
	text := strings.ToLower(t.value)
	return strings.Contains(strings.ToLower(e.Delivery.Path), text) || strings.Contains(strings.ToLower(l.Summary(e.Delivery)), text)
}

// statusMatches takes error for a failure as the counts have it, a class
// such as 4xx, or a code.
func statusMatches(e session.Entry, value string) bool {
	answered, status := e.Target != "" && e.Failure == nil, strconv.Itoa(e.Response.Status)
	switch {
	case value == "error":
		return e.Failed()
	case len(value) == 3 && value[1:] == "xx":
		return answered && status[0] == value[0]
	}
	return answered && status == value
}

// apply filters entries by the typed input; an empty one shows them all.
func (f filter) apply(entries []session.Entry, l cards.Listen) filter {
	text := strings.TrimSpace(f.input)
	f = filter{text: text}
	if text == "" {
		return f
	}
	terms := parseFilter(text)
	for _, e := range entries {
		if matches(e, terms, l) {
			f.numbers = append(f.numbers, e.Number)
		}
	}
	return f
}

// recorded keeps up with the list: evicted requests leave, and a new one
// joins when it matches.
func (f filter) recorded(r session.Recorded, l cards.Listen) filter {
	if f.text == "" {
		return f
	}
	if n := len(r.Evicted); n > 0 {
		f.numbers = f.numbers[sort.SearchInts(f.numbers, r.Evicted[n-1]+1):]
	}
	if matches(r.Entry, parseFilter(f.text), l) {
		f.numbers = append(f.numbers, r.Entry.Number)
	}
	return f
}

func (f filter) shows(n int) bool {
	_, found := slices.BinarySearch(f.numbers, n)
	return f.text == "" || found
}

// editFilter takes keys while the filter line is open: enter applies it, esc
// clears it.
func (m Fullscreen) editFilter(msg tea.KeyPressMsg) Fullscreen {
	switch msg.String() {
	case "enter":
		m.filter = m.filter.apply(m.entries, m.Listen)
	case "esc":
		m.filter = filter{}
	case "backspace":
		runes := []rune(m.filter.input)
		m.filter.input = string(runes[:max(0, len(runes)-1)])
	default:
		m.filter.input += msg.Text
	}
	return m
}

// escape stops w's wait, else dismisses the alerts, else clears the filter.
func (m Fullscreen) escape() Fullscreen {
	switch {
	case m.waiting():
		m.wait = wait{id: m.wait.id}
	case m.reconnected != nil || m.notFound != nil:
		m.reconnected, m.notFound = nil, nil
	default:
		m.filter = filter{}
	}
	return m
}

// shown are the indexes of the entries the filter shows, oldest first.
func (m Fullscreen) shown() []int {
	shown := make([]int, 0, len(m.entries))
	for i, e := range m.entries {
		if m.filter.shows(e.Number) {
			shown = append(shown, i)
		}
	}
	return shown
}

// selectedRow is the selected request's row among shown, -1 when there's
// none. A selection evicted or hidden by the filter falls to the next row,
// else the last.
func (m Fullscreen) selectedRow(shown []int) int {
	if len(shown) == 0 {
		return -1
	}
	if !m.paused {
		return len(shown) - 1
	}
	row := sort.Search(len(shown), func(row int) bool { return m.entries[shown[row]].Number >= m.selected })
	return min(row, len(shown)-1)
}

// filterLines is the filter line while it's typed, with the keys that finish
// it, or applied, with its match count.
func (m Fullscreen) filterLines(width int) []string {
	f := m.filter
	switch {
	case f.editing:
		input := boldStyle.Render("/") + " " + cards.Line(f.input) + selectedStyle.Render(" ")
		return []string{apart(input, faintStyle.Render("status: source: path: or text · ↵ apply · esc clears"), width)}
	case f.text != "":
		count := strconv.Itoa(len(f.numbers)) + " matches"
		if len(f.numbers) == 1 {
			count = "1 match"
		}
		return []string{apart(boldStyle.Render("/")+" "+cards.Line(f.text), faintStyle.Render(count+" · esc clears"), width)}
	}
	return nil
}

// listTitle names the list and counts its requests, shown of all when
// filtered.
func (m Fullscreen) listTitle(shown int) (string, string) {
	if m.filter.text == "" {
		return "Requests", strconv.Itoa(len(m.entries))
	}
	return "Requests" + faintStyle.Render(" · "+cards.Line(m.filter.text)), fmt.Sprintf("%d of %d", shown, len(m.entries))
}

// apart ends a line width wide with right, after left, when both fit.
func apart(left, right string, width int) string {
	if gap := width - lipgloss.Width(left) - lipgloss.Width(right); gap >= 2 {
		return left + strings.Repeat(" ", gap) + right
	}
	return left
}
