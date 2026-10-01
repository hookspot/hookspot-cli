package cards

import (
	"fmt"
	"strings"
	"unicode"
)

// Sanitize escapes terminal control characters in server and delivery text
// so it can't drive the terminal. Newlines and tabs stay for multi-line text
// such as bodies; invalid UTF-8 becomes U+FFFD.
func Sanitize(value string) string {
	return escape(value, false)
}

// Line is Sanitize for single-line fields: newlines and tabs are escaped too,
// so one value can't break a row or a box's width.
func Line(value string) string {
	return escape(value, true)
}

func escape(value string, line bool) string {
	var escaped strings.Builder
	for _, r := range value {
		switch {
		case (r == '\n' || r == '\t') && !line:
			escaped.WriteRune(r)
		case r == '\n':
			escaped.WriteString(`\n`)
		case r == '\t':
			escaped.WriteString(`\t`)
		case r == '\r':
			escaped.WriteString(`\r`)
		case unicode.IsControl(r):
			fmt.Fprintf(&escaped, `\x%02x`, r)
		default:
			escaped.WriteRune(r)
		}
	}
	return escaped.String()
}
