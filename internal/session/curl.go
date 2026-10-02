package session

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"hookspot/internal/proxy"
)

// maxInlineBodyBytes is the largest body a command carries inline. Linux caps
// one argument at 128 KiB, and a bigger body would flood scrollback and the
// clipboard.
const maxInlineBodyBytes = 64 * 1024

// Curl is a request as a curl command for a POSIX shell. Its only control
// characters are line breaks, between arguments and in a body, so pasting it
// can't end a bracketed paste or break argv.
type Curl struct {
	// Command is the full command; only the clipboard gets it.
	Command string
	// Shown is the command to display, with sensitive header values redacted
	// when asked.
	Shown string
	// Redacted is set when Shown hides header values.
	Redacted bool
	// HeadersFile holds the headers with control characters, unredacted; ""
	// when there are none.
	HeadersFile string
	// Resend is set in inspect mode, where the command resends the request
	// through Hookspot.
	Resend bool
}

// Curl builds entry n as a curl command to the URL it was forwarded to, or in
// inspect mode its source's public URL. A body over maxInlineBodyBytes or
// with a control character other than a newline is written to its fixture's
// .body file: pasting turns a carriage return into a newline, and terminals
// copy tabs as spaces. Headers with control characters go to a .headers file
// (read with curl 7.55 and later).
func (s *Session) Curl(n int, redact bool) (Curl, error) {
	entry, err := s.entry(n)
	if err != nil {
		return Curl{}, err
	}
	d := entry.Delivery
	curl := Curl{Resend: !entry.forwarded()}
	target := entry.Target
	if curl.Resend {
		if target = s.publicURL(d.SourceUID); target == "" {
			return Curl{}, fmt.Errorf("#%d: its source has no public URL", n)
		}
	}
	if d.Query != "" {
		target += "?" + d.Query
	}
	if !printable(d.Method+target, "") {
		return Curl{}, fmt.Errorf("#%d: its method or URL has control characters", n)
	}
	method := Method(d)
	if !plainName.MatchString(method) {
		method = Quote(method)
	}
	// -g keeps curl from reading [] and {} in a query as URL globs.
	full := []string{"curl -g -X " + method + " " + Quote(target)}
	shown := slices.Clone(full)

	headers := proxy.Headers(d.Headers)
	headers.Del("Content-Length")
	shownHeaders, _ := redactHeaders(headers, redact)
	var unprintable []string
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		for i, value := range headers[name] {
			if !printable(name+value, "") {
				unprintable = append(unprintable, headerLine(name, value))
				continue
			}
			full = append(full, "-H "+Quote(headerLine(name, value)))
			shown = append(shown, "-H "+Quote(headerLine(name, shownHeaders[name][i])))
		}
	}
	if len(d.Body) > 0 && len(headers["Content-Type"]) == 0 {
		// An empty value drops the form Content-Type curl adds to a body, which
		// forwarding never sends.
		full = append(full, "-H 'Content-Type:'")
		shown = append(shown, "-H 'Content-Type:'")
	}

	var files []string
	name := fixtureName(entry)
	if len(unprintable) > 0 {
		if curl.HeadersFile, err = writeFixture(name+".headers", []byte(strings.Join(unprintable, "\n")+"\n")); err != nil {
			return Curl{}, err
		}
		files = append(files, "-H "+Quote("@"+curl.HeadersFile))
	}
	if body := string(d.Body); body != "" {
		// curl reads a body starting with @ as a file name.
		if len(body) > maxInlineBodyBytes || !printable(body, "\n") || strings.HasPrefix(body, "@") {
			path, err := writeFixture(name+".body", d.Body)
			if err != nil {
				return Curl{}, err
			}
			body = "@" + path
		}
		files = append(files, "--data-binary "+Quote(body))
	}
	curl.Command = strings.Join(append(full, files...), " \\\n  ")
	curl.Shown = strings.Join(append(shown, files...), " \\\n  ")
	curl.Redacted = curl.Shown != curl.Command
	return curl, nil
}

func (s *Session) publicURL(sourceUID string) string {
	for _, source := range s.sources {
		if source.UID == sourceUID {
			return source.URL
		}
	}
	return ""
}

// headerLine is a curl header argument. curl drops a header given an empty
// value, so one is sent as "Name;".
func headerLine(name, value string) string {
	if value == "" {
		return name + ";"
	}
	return name + ": " + value
}

// printable reports whether text is UTF-8 with no control characters but
// those in allowed.
func printable(text, allowed string) bool {
	return utf8.ValidString(text) && !strings.ContainsFunc(text, func(r rune) bool {
		return unicode.IsControl(r) && !strings.ContainsRune(allowed, r)
	})
}

// Quote single-quotes text for a POSIX shell.
func Quote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}
