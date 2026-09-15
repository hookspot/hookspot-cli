package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode"

	"golang.org/x/term"
)

const projectPickerEscapeTimeout = 75 * time.Millisecond

func promptProject(ctx context.Context, in io.Reader, out io.Writer, options []string, defaultIndex int) (selected int, returnErr error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	inFile, inputIsFile := in.(*os.File)
	outFile, outputIsFile := out.(*os.File)
	if !inputIsFile || !outputIsFile || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return 0, errors.New("project selection requires an interactive terminal; pass ORGANIZATION PROJECT or PROJECT_UID")
	}
	if len(options) == 0 {
		return 0, errors.New("project selection has no options")
	}
	if defaultIndex < 0 || defaultIndex >= len(options) {
		defaultIndex = 0
	}
	width, height, err := term.GetSize(int(outFile.Fd()))
	if err != nil {
		return 0, fmt.Errorf("measure project picker terminal: %w", err)
	}
	view, err := newProjectPickerView(width, height, len(options), defaultIndex)
	if err != nil {
		return 0, err
	}

	state, err := term.MakeRaw(int(inFile.Fd()))
	if err != nil {
		return 0, fmt.Errorf("enable project picker: %w", err)
	}
	restoreInput, err := prepareProjectPickerInput(inFile)
	if err != nil {
		_ = term.Restore(int(inFile.Fd()), state)
		return 0, fmt.Errorf("prepare project picker input: %w", err)
	}
	restoreOutput, err := prepareProjectPickerOutput(outFile)
	if err != nil {
		_ = restoreInput()
		_ = term.Restore(int(inFile.Fd()), state)
		return 0, fmt.Errorf("prepare project picker output: %w", err)
	}
	defer func() {
		outputErr := restoreOutput()
		inputErr := restoreInput()
		terminalErr := term.Restore(int(inFile.Fd()), state)
		if returnErr == nil && outputErr != nil {
			returnErr = fmt.Errorf("restore project picker output: %w", outputErr)
		}
		if returnErr == nil && inputErr != nil {
			returnErr = fmt.Errorf("restore project picker input: %w", inputErr)
		}
		if returnErr == nil && terminalErr != nil {
			returnErr = fmt.Errorf("restore terminal: %w", terminalErr)
		}
	}()

	selected = defaultIndex
	if err := view.render(out, options, selected); err != nil {
		return 0, err
	}
	defer func() {
		if err := view.finish(out); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	for {
		key, err := readProjectPickerKey(ctx, inFile)
		if err != nil {
			return 0, err
		}
		next, done, err := applyProjectPickerKey(selected, len(options), key)
		if err != nil {
			return 0, err
		}
		if next != selected {
			selected = next
			if err := view.render(out, options, selected); err != nil {
				return 0, err
			}
		}
		if done {
			return selected, nil
		}
	}
}

type projectPickerView struct {
	width      int
	rows       int
	start      int
	showHeader bool
	rendered   bool
}

func newProjectPickerView(width, height, optionCount, selected int) (*projectPickerView, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("project picker terminal has no usable viewport")
	}
	if optionCount <= 0 || selected < 0 || selected >= optionCount {
		return nil, errors.New("invalid project picker state")
	}
	showHeader := height > 1
	rows := height
	if showHeader {
		rows--
	}
	if rows > optionCount {
		rows = optionCount
	}
	start := selected - rows/2
	if start < 0 {
		start = 0
	}
	if maximum := optionCount - rows; start > maximum {
		start = maximum
	}
	return &projectPickerView{width: width, rows: rows, start: start, showHeader: showHeader}, nil
}

func (view *projectPickerView) render(out io.Writer, options []string, selected int) error {
	if selected < 0 || selected >= len(options) || view.rows <= 0 || view.rows > len(options) {
		return errors.New("invalid project picker state")
	}
	if selected < view.start {
		view.start = selected
	} else if selected >= view.start+view.rows {
		view.start = selected - view.rows + 1
	}
	maximum := len(options) - view.rows
	if view.start > maximum {
		view.start = maximum
	}

	if view.rendered {
		if _, err := fmt.Fprint(out, "\r"); err != nil {
			return fmt.Errorf("write project picker: %w", err)
		}
		if view.rows > 1 {
			if _, err := fmt.Fprintf(out, "\x1b[%dA", view.rows-1); err != nil {
				return fmt.Errorf("write project picker: %w", err)
			}
		}
	} else if view.showHeader {
		header := truncateTerminalText("Select a project (use arrow keys, press Enter):", view.lineWidth())
		if _, err := fmt.Fprintf(out, "\r\x1b[2K%s\r\n", header); err != nil {
			return fmt.Errorf("write project picker: %w", err)
		}
	}

	for row := 0; row < view.rows; row++ {
		index := view.start + row
		line := projectPickerOptionLine(options[index], index == selected, view.lineWidth())
		if _, err := fmt.Fprintf(out, "\r\x1b[2K%s", line); err != nil {
			return fmt.Errorf("write project picker: %w", err)
		}
		if row+1 < view.rows {
			if _, err := fmt.Fprint(out, "\r\n"); err != nil {
				return fmt.Errorf("write project picker: %w", err)
			}
		}
	}
	view.rendered = true
	return nil
}

func (view *projectPickerView) finish(out io.Writer) error {
	if !view.rendered {
		return nil
	}
	view.rendered = false
	if _, err := fmt.Fprint(out, "\r\n"); err != nil {
		return fmt.Errorf("write project picker: %w", err)
	}
	return nil
}

func (view *projectPickerView) lineWidth() int {
	if view.width <= 1 {
		return 1
	}
	return view.width - 1
}

func projectPickerOptionLine(option string, selected bool, maximumWidth int) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	if maximumWidth <= 0 {
		return ""
	}
	if maximumWidth <= 2 {
		return string([]rune(marker)[:maximumWidth])
	}
	return marker + truncateTerminalText(option, maximumWidth-2)
}

func truncateTerminalText(value string, maximumWidth int) string {
	if maximumWidth <= 0 {
		return ""
	}
	if terminalTextWidth(value) <= maximumWidth {
		return value
	}
	ellipsis := "…"
	ellipsisWidth := terminalTextWidth(ellipsis)
	if maximumWidth < ellipsisWidth {
		return "."
	}

	limit := maximumWidth - ellipsisWidth
	width := 0
	runes := make([]rune, 0, len(value))
	for _, r := range value {
		runeWidth := terminalRuneWidth(r)
		if width+runeWidth > limit {
			break
		}
		runes = append(runes, r)
		width += runeWidth
	}
	return string(runes) + ellipsis
}

func terminalTextWidth(value string) int {
	width := 0
	for _, r := range value {
		width += terminalRuneWidth(r)
	}
	return width
}

func terminalRuneWidth(r rune) int {
	if r == '\u20e3' {
		return 1
	}
	if r == 0 || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if unicode.IsControl(r) {
		return 0
	}
	if r < unicode.MaxASCII {
		return 1
	}
	// Reserve two cells for every non-ASCII printable rune. This deliberately
	// truncates some narrow characters early, but it keeps labels bounded
	// without relying on an incomplete, terminal-specific Unicode width table.
	return 2
}

func parseProjectPickerKey(ctx context.Context, readByte func(context.Context) (byte, error), escapeTimeout time.Duration) ([]byte, error) {
	first, err := readByte(ctx)
	if err != nil {
		return nil, err
	}
	if first != '\x1b' {
		return []byte{first}, nil
	}

	key := []byte{first}
	second, timedOut, err := readProjectPickerEscapeByte(ctx, readByte, escapeTimeout)
	if err != nil {
		return nil, err
	}
	if timedOut {
		return key, nil
	}
	if second == 3 {
		return []byte{3}, nil
	}
	key = append(key, second)
	if second != '[' && second != 'O' {
		return key, nil
	}
	third, timedOut, err := readProjectPickerEscapeByte(ctx, readByte, escapeTimeout)
	if err != nil {
		return nil, err
	}
	if timedOut {
		return key[:1], nil
	}
	if third == 3 {
		return []byte{3}, nil
	}
	return append(key, third), nil
}

func readProjectPickerEscapeByte(ctx context.Context, readByte func(context.Context) (byte, error), timeout time.Duration) (byte, bool, error) {
	continuationContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	value, err := readByte(continuationContext)
	if err == nil {
		return value, false, nil
	}
	if ctx.Err() != nil {
		return 0, false, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 0, true, nil
	}
	return 0, false, err
}

func applyProjectPickerKey(current, optionCount int, key []byte) (int, bool, error) {
	if optionCount <= 0 || current < 0 || current >= optionCount {
		return current, false, errors.New("invalid project picker state")
	}
	switch {
	case bytes.Equal(key, []byte("\x1b[A")), bytes.Equal(key, []byte("\x1bOA")):
		return (current - 1 + optionCount) % optionCount, false, nil
	case bytes.Equal(key, []byte("\x1b[B")), bytes.Equal(key, []byte("\x1bOB")):
		return (current + 1) % optionCount, false, nil
	case bytes.Equal(key, []byte{'\r'}), bytes.Equal(key, []byte{'\n'}):
		return current, true, nil
	case bytes.Equal(key, []byte{3}), bytes.Equal(key, []byte{'\x1b'}):
		return current, false, context.Canceled
	default:
		return current, false, nil
	}
}
