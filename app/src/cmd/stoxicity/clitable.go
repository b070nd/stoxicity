package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// cliStyle is how a cell of a command-line table is coloured. Green, yellow and
// red are a traffic light: green is favourable or ordinary, yellow needs a
// look, red is adverse or high.
type cliStyle int

const (
	cliPlain cliStyle = iota
	cliGreen
	cliYellow
	cliRed
	cliDim
	cliStrong
)

var cliCodes = map[cliStyle]string{
	cliGreen:  "\x1b[32m",
	cliYellow: "\x1b[33m",
	cliRed:    "\x1b[31m",
	cliDim:    "\x1b[2m",
	cliStrong: "\x1b[1m",
}

// cliCell is one cell: its text, kept whole (never cut or abbreviated), and style.
type cliCell struct {
	Text  string
	Style cliStyle
}

func plain(s string) cliCell               { return cliCell{Text: s} }
func styled(s string, st cliStyle) cliCell { return cliCell{Text: s, Style: st} }

// cliTable is a table with a header, a rule under it and columns that are as
// wide as their widest cell.
type cliTable struct {
	Header []string
	Right  []bool // right-aligned columns (numbers)
	Rows   [][]cliCell
}

// useColor says whether to colour output written to w: only on a terminal, and
// not when NO_COLOR is set.
func useColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && os.Getenv("NO_COLOR") == "" && term.IsTerminal(f.Fd())
}

func (t cliTable) write(w io.Writer, color bool) {
	widths := make([]int, len(t.Header))
	for i, h := range t.Header {
		widths[i] = ansi.StringWidth(h)
	}
	for _, r := range t.Rows {
		for i, c := range r {
			if i < len(widths) {
				widths[i] = max(widths[i], ansi.StringWidth(c.Text))
			}
		}
	}
	paint := func(s string, st cliStyle) string {
		if code := cliCodes[st]; color && code != "" {
			return code + s + "\x1b[0m"
		}
		return s
	}
	pad := func(c cliCell, i int, last bool) string {
		gap := widths[i] - ansi.StringWidth(c.Text)
		switch {
		case i < len(t.Right) && t.Right[i]:
			return strings.Repeat(" ", gap) + paint(c.Text, c.Style)
		case last:
			return paint(c.Text, c.Style) // no trailing spaces on the last column
		}
		return paint(c.Text, c.Style) + strings.Repeat(" ", gap)
	}
	line := func(cells []cliCell) {
		parts := make([]string, len(widths))
		for i := range widths {
			c := plain("")
			if i < len(cells) {
				c = cells[i]
			}
			parts[i] = pad(c, i, i == len(widths)-1)
		}
		fmt.Fprintln(w, strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	head := make([]cliCell, len(t.Header))
	rule := make([]cliCell, len(t.Header))
	for i, h := range t.Header {
		head[i] = styled(h, cliStrong)
		rule[i] = styled(strings.Repeat("─", widths[i]), cliDim)
	}
	line(head)
	line(rule)
	for _, r := range t.Rows {
		line(r)
	}
}

// toneStyle is the traffic light of a tone from -1 to +1.
func toneLight(v float64) cliStyle {
	switch {
	case v > 0.05:
		return cliGreen
	case v < -0.05:
		return cliRed
	}
	return cliYellow
}

// materialityLight is the traffic light of a materiality from 0 to 5: the more
// material the news, the more it needs attention.
func materialityLight(v float64) cliStyle {
	switch {
	case v >= 4:
		return cliRed
	case v >= 3:
		return cliYellow
	}
	return cliGreen
}

// kindLight marks the kind of security: an ordinary stock is green, a fund
// yellow, and the kinds that behave differently from a stock red.
func kindLight(kind string) cliStyle {
	switch strings.ToLower(kind) {
	case "stock":
		return cliGreen
	case "etf", "fund":
		return cliYellow
	case "":
		return cliDim
	}
	return cliRed
}

const cliLegend = "colours: green is favourable or ordinary, yellow needs a look, red is adverse or high"
