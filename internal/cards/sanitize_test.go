package cards

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSanitizeAndLine(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		sanitize string
		line     string
	}{
		{name: "plain text", in: "café ✓ 200", sanitize: "café ✓ 200", line: "café ✓ 200"},
		{name: "newline and tab", in: "a\tb\nc", sanitize: "a\tb\nc", line: `a\tb\nc`},
		{name: "carriage return", in: "a\r\nb", sanitize: "a\\r\nb", line: `a\r\nb`},
		{name: "C0 escape sequence", in: "\x1b[31mred\x1b[0m", sanitize: `\x1b[31mred\x1b[0m`, line: `\x1b[31mred\x1b[0m`},
		{name: "NUL, BEL and DEL", in: "\x00\x07\x7f", sanitize: `\x00\x07\x7f`, line: `\x00\x07\x7f`},
		{name: "C1 CSI and NEL", in: "\u009b31m\u0085", sanitize: `\x9b31m\x85`, line: `\x9b31m\x85`},
		{name: "invalid UTF-8", in: "a\xffb\x9b\xc2", sanitize: "a�b��", line: "a�b��"},
		{name: "error text", in: "bad\x1b[31m\rvalue", sanitize: `bad\x1b[31m\rvalue`, line: `bad\x1b[31m\rvalue`},
		{name: "body text", in: "hello\tworld\x1b[31m", sanitize: "hello\tworld\\x1b[31m", line: `hello\tworld\x1b[31m`},
		{name: "display text", in: "line\ncolumn\t\x1b", sanitize: "line\ncolumn\t\\x1b", line: `line\ncolumn\t\x1b`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Sanitize(test.in); got != test.sanitize {
				t.Errorf("Sanitize(%q) = %q, want %q", test.in, got, test.sanitize)
			}
			if got := Line(test.in); got != test.line {
				t.Errorf("Line(%q) = %q, want %q", test.in, got, test.line)
			}
		})
	}
}

func TestLineLeavesNoControlCharacters(t *testing.T) {
	var all []byte
	for b := range 256 {
		all = append(all, byte(b))
	}
	for r := rune(0x80); r <= 0x9f; r++ {
		all = utf8.AppendRune(all, r)
	}

	got := Line(string(all))
	if !utf8.ValidString(got) {
		t.Fatalf("Line output is not valid UTF-8: %q", got)
	}
	for _, r := range got {
		if unicode.IsControl(r) {
			t.Fatalf("Line output contains control character %U: %q", r, got)
		}
	}
}
