package cards

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
)

func TestPalette(t *testing.T) {
	var out strings.Builder
	for _, uid := range []string{"src_stripe", "src_github", "src_shopify"} {
		out.WriteString(lipgloss.NewStyle().Foreground(SourceColor(uid)).Render("● "+uid) + "\n")
	}
	for _, status := range []int{200, 307, 404, 500, 0} {
		out.WriteString(ColorBadge(StatusColor(status), strconv.Itoa(status)) + "\n")
	}
	out.WriteString(Badge("POST") + "\n")
	golden.RequireEqual(t, out.String())
}

func TestWidthFallsBackWhenNotATerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	for _, out := range []io.Writer{&bytes.Buffer{}, writer} {
		if got := Width(out); got != DefaultWidth {
			t.Errorf("Width(%T) = %d, want %d", out, got, DefaultWidth)
		}
	}
}
