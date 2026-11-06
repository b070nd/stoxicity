package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/sanitize"
	"github.com/b070nd/stoxicity/app/src/status"
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
	styleFresh  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
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
	lines := []string{m.watchBar(), m.meterLine(), m.crumbs()}
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

// meterLine is the header: how the sources are doing, when the next fetch is
// due, how much news the window holds and what is unread.
func (m *Model) meterLine() string {
	var parts []string
	if m.haveSources {
		mt := status.Summarise(m.sources)
		if mt.Total == 0 {
			parts = append(parts, styleDim.Render("no sources"))
		} else {
			s := "sources " + styleGood.Render(fmt.Sprintf("%d ok", mt.Green))
			if mt.Yellow > 0 {
				s += " " + styleWarn.Render(fmt.Sprintf("%d stale", mt.Yellow))
			}
			if mt.Red > 0 {
				s += " " + styleBad.Render(fmt.Sprintf("%d failing", mt.Red))
			}
			if mt.Unknown > 0 {
				s += " " + styleDim.Render(fmt.Sprintf("%d not polled", mt.Unknown))
			}
			parts = append(parts, s)
			if mt.Next != "" {
				in := mt.NextDue.Sub(m.be.Now())
				if in >= 0 {
					parts = append(parts, fmt.Sprintf("next fetch %s in %s", mt.Next, shortDur(in)))
				} else {
					parts = append(parts, styleWarn.Render(fmt.Sprintf("%s overdue %s", mt.Next, shortDur(-in))))
				}
			}
		}
	}
	if m.counts.ok {
		parts = append(parts, fmt.Sprintf("news %s articles, %s relevant", thousands(m.counts.articles), thousands(m.counts.relevant)))
	}
	unread := 0
	for _, w := range m.watches {
		unread += w.Unread
	}
	if unread > 0 {
		parts = append(parts, styleBold.Render(fmt.Sprintf("%s unread", thousands(unread))))
	}
	return " " + strings.Join(parts, styleDim.Render(" · "))
}

// shortDur writes a duration as m:ss, or h:mm from an hour.
func shortDur(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02dh", int(d/time.Hour), int(d/time.Minute)%60)
	}
	return fmt.Sprintf("%d:%02d", int(d/time.Minute), int(d/time.Second)%60)
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
	if len(m.scr.Rows) == 0 {
		if m.scr.Notice != "" {
			out = append(out, "", styleDim.Render(" "+clean(m.scr.Notice)))
		}
		return out
	}
	at, ok := m.vp.Row()
	if !ok {
		at = -1
	}
	from, end := m.vp.Window()
	for i := from; i < end; i++ {
		row := m.scr.Rows[i]
		text := clean(row.Text)
		mark := " "
		if t := markable(row); t != "" && m.marks[t] {
			mark = "●"
		}
		if i == at {
			out = append(out, styleCursor.Render(fit("▸"+mark+text, m.width)))
			continue
		}
		style := row.Style.lip()
		if m.isFresh(row) {
			style = styleFresh
		}
		out = append(out, style.Render(" "+mark+text))
	}
	return out
}

func (m *Model) filterLine() string {
	switch m.mode {
	case modeFilter, modeTag, modeMember, modeJump:
		return m.prompt.Line()
	}
	return m.keyBar()
}

// keyBar lists the keys that do something here, in the order of the key map.
// When they do not all fit, the ones at the end of the list go first, except
// help and quit, which stay.
func (m *Model) keyBar() string {
	c := m.context()
	type item struct{ key, label string }
	var items, tail []item
	for _, b := range DefaultKeys {
		if b.Bar == "" || !b.Applies(c) {
			continue
		}
		it := item{keyName(b.Keys[0]), b.Bar}
		if b.Action == Help || b.Action == Quit {
			tail = append(tail, it)
		} else {
			items = append(items, it)
		}
	}
	width := func(list ...[]item) int {
		n := 1
		for _, l := range list {
			for _, it := range l {
				n += utf8.RuneCountInString(it.key) + 1 + utf8.RuneCountInString(it.label) + 2
			}
		}
		return n
	}
	for len(items) > 0 && width(items, tail) > m.width {
		items = items[:len(items)-1]
	}
	var b strings.Builder
	b.WriteString(" ")
	for _, it := range append(items, tail...) {
		b.WriteString(styleBold.Render(it.key) + styleDim.Render(" "+it.label) + "  ")
	}
	return strings.TrimRight(b.String(), " ")
}

func (m *Model) statusLine() string {
	var parts []string
	if m.loading {
		parts = append(parts, "loading…")
	} else if !m.refreshed.IsZero() {
		parts = append(parts, "refreshed "+m.refreshed.In(m.be.Zone()).Format("15:04"))
	}
	if m.stack.Current().Kind == nav.Companies && m.vp.Len() > 0 {
		if pos := m.companyPos(); pos > 0 {
			parts = append(parts, fmt.Sprintf("company %s of %s", thousands(pos), thousands(m.companies)))
		} else {
			parts = append(parts, "enter opens the group")
		}
	}
	if n := len(m.marks); n > 0 {
		parts = append(parts, fmt.Sprintf("%d marked", n))
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
	// Two columns, filled down the left one first, so the whole map shows on
	// a screen of ordinary height.
	col := max((m.width-2)/2, 20)
	entries := make([]string, len(DefaultKeys))
	for i, b := range DefaultKeys {
		keys := strings.Join(b.Keys, " ")
		if len(b.Keys) > 3 {
			keys = b.Keys[0] + " … " + b.Keys[len(b.Keys)-1]
		}
		entries[i] = clip(fmt.Sprintf("%-14s %s", keys, b.Help), col-2)
	}
	half := (len(entries) + 1) / 2
	for i := range half {
		line := fmt.Sprintf("  %-*s", col, entries[i])
		if j := i + half; j < len(entries) {
			line += entries[j]
		}
		out = append(out, strings.TrimRight(line, " "))
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
