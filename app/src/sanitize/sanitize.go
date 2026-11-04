// Package sanitize cleans text from the internet before it is stored, drawn in
// the terminal, sent by email or shown to a language model. Feeds can carry
// escape sequences that move the cursor or set the window title, and invisible
// Unicode that reorders or hides text: bidirectional overrides that make a
// filename or a ticker read backwards, zero-width characters, and "tag"
// characters (U+E0000 range) that can carry hidden instructions.
package sanitize

import (
	"regexp"
	"strings"
	"unicode"
)

// ansi matches CSI sequences (ESC [ ... final byte), string sequences that run
// to a terminator (OSC, DCS, SOS, PM and APC: ESC ] P X ^ _ ... BEL or ESC \),
// and two-byte escapes. An unterminated string sequence is removed to the end
// of the text, so its payload cannot show as plain text.
var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]` +
	`|\x1b[\]PX^_](?s:.*?)(?:\x07|\x1b\\|\z)` +
	`|\x1b[@-_]`)

// Text removes escape sequences, control characters and invisible formatting
// characters, keeps newlines and tabs, turns line and paragraph separators
// into newlines, and trims the ends.
//
// Dropping every format character (Unicode category Cf) also removes the
// zero-width joiner, so emoji sequences fall apart into their parts, and the
// zero-width non-joiner that some Persian and Indic text needs. For English
// financial news that is the right trade; revisit it before using the package
// for other languages.
func Text(s string) string {
	s = ansi.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == ' ' || r == ' ':
			return '\n'
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}
