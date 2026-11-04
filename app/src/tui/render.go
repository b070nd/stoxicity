package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/sanitize"
)

// The palette uses the terminal's own 16 colours, so it follows the user's
// theme, light or dark.
var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleGood   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleBad    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleCursor = lipgloss.NewStyle().Reverse(true)
	styleTab    = lipgloss.NewStyle().Reverse(true).Bold(true)
	styleHead   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	cloud       = [...]lipgloss.Style{
		lipgloss.NewStyle().Faint(true),
		lipgloss.NewStyle(),
		lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true),
	}
)

func (s Style) lip() lipgloss.Style {
	switch s {
	case Heading:
		return styleHead
	case Dim:
		return styleDim
	case Good:
		return styleGood
	case Bad:
		return styleBad
	case Warn:
		return styleWarn
	case Cloud1, Cloud2, Cloud3, Cloud4, Cloud5:
		return cloud[s-Cloud1]
	}
	return lipgloss.NewStyle()
}

// clean makes text from the internet safe to draw on one line.
func clean(s string) string { return oneLine(sanitize.Text(s)) }

// fit cuts s to w cells and pads it to w with spaces.
func fit(s string, w int) string {
	s = lipgloss.NewStyle().MaxWidth(w).Render(s)
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// View draws the whole screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m *Model) render() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("Terminal too small (%d×%d); need %d×%d.", m.width, m.height, minWidth, minHeight)
	}
	var body []string
	switch {
	case m.mode == modeHelp:
		body = m.helpLines()
	case m.detail != nil:
		body = m.detailLines()
	default:
		body = m.bodyLines()
	}
	h := m.bodyHeight()
	for len(body) < h {
		body = append(body, "")
	}
	body = body[:h]
	lines := []string{m.watchBar(), m.crumbs()}
	lines = append(lines, body...)
	lines = append(lines, m.filterLine(), m.statusLine())
	for i, l := range lines {
		lines[i] = fit(l, m.width)
	}
	return strings.Join(lines, "\n")
}

// watchBar is one tab per watch with its unread badge.
func (m *Model) watchBar() string {
	cur := m.stack.Watch()
	tab := func(label string, active bool) string {
		if active {
			return styleTab.Render(" " + label + " ")
		}
		return " " + label + " "
	}
	parts := []string{tab("All news", cur == "")}
	for i, w := range m.watches {
		label := clean(w.Name)
		if i < 9 {
			label = fmt.Sprintf("%d %s", i+1, label)
		}
		if w.Unread > 0 && w.ID != cur {
			label += fmt.Sprintf(" (%d)", w.Unread)
		}
		parts = append(parts, tab(label, w.ID == cur))
	}
	return strings.Join(parts, "")
}

func (m *Model) crumbs() string {
	cs := m.stack.Crumbs()
	parts := make([]string, len(cs))
	for i, c := range cs {
		l := clean(c.Label)
		if i == len(cs)-1 {
			l = styleBold.Render(l)
		}
		parts[i] = l
	}
	return " " + strings.Join(parts, styleDim.Render(" › "))
}

// bodyLines draws the visible rows of the screen.
func (m *Model) bodyLines() []string {
	var out []string
	out = append(out, styleBold.Render(" "+clean(m.scr.Title)))
	h := m.bodyHeight()
	if len(m.scr.Rows) == 0 {
		if m.scr.Notice != "" {
			out = append(out, "", styleDim.Render(" "+clean(m.scr.Notice)))
		}
		return out
	}
	at := -1
	if m.cursor < len(m.sel) {
		at = m.sel[m.cursor]
	}
	end := min(m.top+h-1, len(m.scr.Rows))
	for i := m.top; i < end; i++ {
		row := m.scr.Rows[i]
		text := clean(row.Text)
		if i == at {
			out = append(out, styleCursor.Render(fit("▸ "+text, m.width)))
			continue
		}
		out = append(out, row.Style.lip().Render("  "+text))
	}
	return out
}

func (m *Model) filterLine() string {
	if m.mode == modeFilter {
		return " / " + string(m.filter) + "█"
	}
	return styleDim.Render(" / filter · ? help")
}

func (m *Model) statusLine() string {
	var parts []string
	if m.loading {
		parts = append(parts, "loading…")
	} else if !m.refreshed.IsZero() {
		parts = append(parts, "refreshed "+m.refreshed.In(m.be.Zone()).Format("15:04"))
	}
	parts = append(parts, m.be.Tenant())
	line := " " + strings.Join(parts, " · ")
	if m.status != "" {
		msg := clean(m.status)
		if m.failed {
			msg = styleBad.Render(msg)
		}
		line += " · " + msg
	}
	return line
}

func (m *Model) helpLines() []string {
	out := []string{styleBold.Render(" Keys"), ""}
	for _, b := range DefaultKeys {
		keys := strings.Join(b.Keys, " ")
		if len(b.Keys) > 3 {
			keys = b.Keys[0] + " … " + b.Keys[len(b.Keys)-1]
		}
		out = append(out, fmt.Sprintf("  %-18s %s", keys, b.Help))
	}
	out = append(out, "", styleDim.Render(" press any key to close"))
	return out
}

// wrap breaks text into lines of at most w cells, at spaces.
func wrap(text string, w int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for utf8.RuneCountInString(word) > w {
				r := []rune(word)
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, string(r[:w]))
				word = string(r[w:])
			}
			switch {
			case line == "":
				line = word
			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// detailLines draws one article in full, with the reasons for its score.
func (m *Model) detailLines() []string {
	a := m.detail
	w := m.width - 4
	zone := m.be.Zone()
	var out []string
	add := func(s string) { out = append(out, "  "+s) }
	for _, l := range wrap(sanitize.Text(a.Title), w) {
		add(styleBold.Render(l))
	}
	add(styleDim.Render(clean(fmt.Sprintf("%s · %s · %s", a.Domain, a.SourceID, a.Published.In(zone).Format("2006-01-02 15:04 MST")))))
	add(styleDim.Render(clean(a.URL)))
	out = append(out, "")
	s := a.Score
	add(fmt.Sprintf("relevance %.2f · materiality %d · tone %s · %s · %s", s.Relevance, s.Materiality, tonePct(s.Tone), emptyDash(string(s.Kind)), emptyDash(s.Sector)))
	if len(a.Tickers) > 0 {
		add("stocks: " + clean(strings.Join(a.Tickers, " ")))
	}
	if len(a.Topics) > 0 {
		add("topics: " + clean(strings.Join(a.Topics, " ")))
	}
	if a.Form != "" {
		add("form: " + clean(a.Form+" "+strings.Join(a.Items, " ")))
	}
	add("your label: " + labelWord(m.verdicts[a.ID]))
	if len(s.Reasons) > 0 {
		out = append(out, "")
		add(styleHead.Render("Why this score"))
		for _, r := range s.Reasons {
			for i, l := range wrap(sanitize.Text(r), w-2) {
				if i == 0 {
					l = "• " + l
				} else {
					l = "  " + l
				}
				add(l)
			}
		}
	}
	if a.Summary != "" {
		out = append(out, "")
		for _, l := range wrap(sanitize.Text(a.Summary), w) {
			add(l)
		}
	}
	out = append(out, "", styleDim.Render("  k keep · d drop · o open in browser · esc back"))
	return out
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func labelWord(v labels.Verdict) string {
	switch v {
	case labels.Keep:
		return "kept"
	case labels.Drop:
		return "dropped"
	}
	return "none"
}
