package sanitize

import "testing"

func TestText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "Apple beats estimates", "Apple beats estimates"},
		{"colour", "\x1b[31mred\x1b[0m", "red"},
		{"cursor move", "a\x1b[2Jb", "ab"},
		{"window title, BEL end", "x\x1b]0;owned\x07y", "xy"},
		{"window title, ST end", "x\x1b]0;owned\x1b\\y", "xy"},
		{"unterminated OSC drops its payload", "x\x1b]0;owned", "x"},
		{"device control string", "x\x1bPq#0;2;0;0;0\x1b\\y", "xy"},
		{"application program command", "x\x1b_payload\x1b\\y", "xy"},
		{"two-byte escape", "a\x1bMb", "ab"},
		{"control chars", "a\x00b\x07c", "abc"},
		{"C1 control (8-bit CSI)", "a\u009b2Jb", "a2Jb"},
		{"keeps newline and tab", " a\n\tb ", "a\n\tb"},
		{"line separators become newlines", "a\u2028b\u2029c", "a\nb\nc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in); got != c.want {
				t.Fatalf("Text(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTextRemovesInvisibleUnicode(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"right-to-left override", "invoice\u202eFDP.exe", "invoiceFDP.exe"},
		{"bidi isolates", "a\u2066b\u2069c", "abc"},
		{"zero-width space", "AA\u200bPL", "AAPL"},
		{"zero-width no-break space (BOM)", "\ufeffAAPL", "AAPL"},
		{"word joiner", "A\u2060B", "AB"},
		{"unicode tag characters", "ok\U000e0049\U000e0067\U000e006eore", "okore"},
		{"soft hyphen", "co\u00adop", "coop"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in); got != c.want {
				t.Fatalf("Text(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTextKeepsOrdinaryUnicode(t *testing.T) {
	for _, s := range []string{"Nestlé raises dividend", "日本銀行 policy", "€5.2bn — up 3%", "résumé café"} {
		if got := Text(s); got != s {
			t.Errorf("Text(%q) = %q; ordinary text must pass through", s, got)
		}
	}
}
