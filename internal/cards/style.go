package cards

import (
	"hash/fnv"
	"image/color"
	"io"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// The palette sticks to the 16 ANSI colors so cards follow the terminal's own
// theme, light or dark.
var sourceColors = [...]color.Color{
	lipgloss.Green, lipgloss.Yellow, lipgloss.Blue, lipgloss.Magenta, lipgloss.Cyan,
	lipgloss.BrightGreen, lipgloss.BrightYellow, lipgloss.BrightBlue, lipgloss.BrightMagenta, lipgloss.BrightCyan,
}

var (
	badgeStyle      = lipgloss.NewStyle().Padding(0, 1)
	plainBadgeStyle = badgeStyle.Foreground(lipgloss.BrightWhite).Background(lipgloss.BrightBlack)
	colorBadgeStyle = badgeStyle.Foreground(lipgloss.Black)

	faintStyle = lipgloss.NewStyle().Faint(true)
	boldStyle  = lipgloss.NewStyle().Bold(true)
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Green)
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

// DefaultWidth is the card width when the output isn't a terminal or its size
// is unknown.
const DefaultWidth = 100

// SourceColor gives a source the same color on every run.
func SourceColor(uid string) color.Color {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(uid))
	return sourceColors[hash.Sum32()%uint32(len(sourceColors))]
}

// StatusColor colors an HTTP status. Anything outside 2xx and 3xx, including
// a transport failure (0), is a failure.
func StatusColor(status int) color.Color {
	switch {
	case status >= 200 && status < 300:
		return lipgloss.Green
	case status >= 300 && status < 400:
		return lipgloss.Yellow
	default:
		return lipgloss.Red
	}
}

// Badge renders a neutral chip, such as a method or the test mark.
func Badge(text string) string {
	return plainBadgeStyle.Render(text)
}

// ColorBadge renders a chip on bg, such as a status or a connection state.
func ColorBadge(bg color.Color, text string) string {
	return colorBadgeStyle.Background(bg).Render(text)
}

// Terminal reports whether out is a terminal.
func Terminal(out io.Writer) bool {
	file, ok := out.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

// Width is out's terminal width, or DefaultWidth when unknown.
func Width(out io.Writer) int {
	if file, ok := out.(interface{ Fd() uintptr }); ok {
		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return DefaultWidth
}
