package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
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
	styleHot    = lipgloss.NewStyle().Bold(true).Underline(true) // a key's letter inside its word
	styleHead   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	cloud       = [...]lipgloss.Style{
		lipgloss.NewStyle().Faint(true),
		lipgloss.NewStyle(),
		lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true),
	}
)

// hues are the colours of the ten SIC divisions: the terminal's own palette,
// the bright ones where the plain ones would repeat.
var hues = [10]string{"2", "3", "1", "4", "6", "5", "10", "11", "12", "13"}

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
	case Strong:
		return styleBold
	case Hot:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	case Info:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	case Accent:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	case Blue:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	case Hue1, Hue2, Hue3, Hue4, Hue5, Hue6, Hue7, Hue8, Hue9, Hue10:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(hues[s-Hue1]))
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
	if m.mode == modeFilter && len(m.suggestions) > 0 {
		suggestions := m.suggestionLines()
		n := min(len(suggestions), max(h-1, 0))
		copy(body[h-n:], suggestions[:n])
	}
	lines := []string{m.watchBar(), m.scopeLine(), m.meterLine(), m.crumbs()}
	lines = append(lines, body...)
	lines = append(lines, m.filterLine(), m.statusLine())
	for i, l := range lines {
		lines[i] = fit(l, m.width)
	}
	return strings.Join(lines, "\n")
}

// screenTabs are the screens that stand on their own, after the watches: each
// is one key away, and the one the user is in (or is under, for a stock, a topic
// or an exchange opened from it) is lit. The screens that need something to show
// (a stock, a topic, a domain, a day, a search) are reached from these.
var screenTabs = []struct {
	label string
	key   string
	kinds []nav.Kind
}{
	{"News", "n", []nav.Kind{nav.Home, nav.Search, nav.Day}},
	{"Stocks", "s", []nav.Kind{nav.Stocks, nav.Companies, nav.Stock}},
	{"Topics", "t", []nav.Kind{nav.Topics}},
	{"Domains", "d", []nav.Kind{nav.Domains}},
	{"Exchanges", "e", []nav.Kind{nav.Exchanges}},
	{"Sources", "o", []nav.Kind{nav.Sources}},
	{"Watches", "w", []nav.Kind{nav.Watches}},
	{"Label", "a", []nav.Kind{nav.Label}},
	{"Plan", "p", []nav.Kind{nav.Plans, nav.Plan, nav.Policy}},
	{"Portfolio", "f", []nav.Kind{nav.Portfolio, nav.Book, nav.Orders, nav.Order}},
	{"Income", "m", []nav.Kind{nav.Income}},
}

// stocksQuery is what the s key and the Stocks tab open: every stock, those in
// the news first.
const stocksQuery = "sort:news group:none"

// litTab is the screen tab the user is in: the section of the trail.
func (m *Model) litTab() string {
	trail := m.stack.Crumbs()
	r := trail[0].Route
	if len(trail) > 1 {
		r = trail[1].Route
	}
	for _, t := range screenTabs {
		if slices.Contains(t.kinds, r.Kind) {
			return t.label
		}
	}
	// A detail opened straight from Home belongs to the list it normally sits under.
	if p, ok := r.Parent(); ok {
		for _, t := range screenTabs {
			if slices.Contains(t.kinds, p.Kind) {
				return t.label
			}
		}
	}
	return ""
}

// watchBar contains only sections. Watch and relevance are filters below it.
func (m *Model) watchBar() string {
	var parts []string
	lit := m.litTab()
	for _, t := range screenTabs {
		style := styleDim
		if t.label == lit {
			style = styleTab
			if m.navFocus {
				style = style.Foreground(lipgloss.Color("6")).Underline(true)
			}
		}
		parts = append(parts, style.Render(" ")+hotLabel(t.key, t.label, style, style.Underline(true))+style.Render(" "))
	}
	// Keep the active section visible when the terminal cannot fit the whole bar.
	active := 0
	for i, t := range screenTabs {
		if t.label == lit {
			active = i
		}
	}
	start, end := 0, len(parts)
	for lipgloss.Width(strings.Join(parts[start:end], " ")) > m.width-2 && end-start > 1 {
		if start < active {
			start++
		} else {
			end--
		}
	}
	left, right := " ", ""
	if start > 0 {
		left = "‹"
	}
	if end < len(parts) {
		right = "›"
	}
	return left + strings.Join(parts[start:end], " ") + right
}

// meterLine is the header: how the sources are doing, when the next fetch is
// due, how much news the window holds and what is unread.
func (m *Model) meterLine() string {
	var parts []string
	if m.exchange != "" {
		parts = append(parts, styleBold.Render("exchange "+clean(m.exchange)))
	} else {
		parts = append(parts, styleDim.Render("all exchanges"))
	}
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
		if c.Route.Kind == nav.Home {
			l = "News"
		}
		if c.Route.Kind == nav.Companies {
			l = "Stocks"
		}
		if i == len(cs)-1 {
			if m.scr.Title != "" {
				l = clean(m.scr.Title)
			}
			l = styleBold.Render(l)
		}
		parts[i] = l
	}
	return " " + strings.Join(parts, styleDim.Render(" › "))
}

// bodyLines draws the visible rows of the screen.
func (m *Model) bodyLines() []string {
	var out []string
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
	for i := 0; i < from; i++ {
		if m.scr.Rows[i].TableHeader {
			out = append(out, styleHead.Render("  "+m.scr.Rows[i].Text))
			break
		}
	}
	for i := from; i < end; i++ {
		row := m.scr.Rows[i]
		if len(row.Chips) > 0 {
			out = append(out, m.chipLine(i, row))
			continue
		}
		text := safeLine(row.Text)
		mark := " "
		if t := markable(row); t != "" && m.marks[t] {
			mark = "●"
		}
		style := row.Style.lip()
		if m.isFresh(row) {
			style = style.Bold(true) // freshness must not turn a negative figure green
		}
		lead := " " + mark
		if i == at && !m.navFocus && !m.chipFocus {
			style = style.Reverse(true)
			lead = "▸" + mark
		}
		if len(row.Spans) > 0 && text == row.Text {
			out = append(out, spanned(style, lead, text, row.Spans))
			continue
		}
		out = append(out, style.Render(lead+text))
	}
	return out
}

func (m *Model) filterLine() string {
	switch m.mode {
	case modeFilter, modeTag, modeMember, modeJump, modeConfirm:
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
	if m.navFocus {
		return " " + styleDim.Render(m.navigationHint()+"  v relevant  ? help  q quit")
	}
	switch {
	case m.chipFocus && m.detail == nil:
		items = append(items, item{"← →", "exchange"}, item{"enter", "pick"}, item{"↓", "back to the list"})
	case c.Detail || m.vp.Cursor() > 0 || !m.hasChips():
	default:
		items = append(items, item{"↑", "exchanges"})
	}
	for _, b := range DefaultKeys {
		if m.chipFocus && m.detail == nil && b.Bar != "" && b.Action != Help && b.Action != Quit {
			continue
		}
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
				n += hotWidth(it.key, it.label) + 2
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
		b.WriteString(hotLabel(it.key, it.label, styleDim, styleHot) + "  ")
	}
	return strings.TrimRight(b.String(), " ")
}

func (m *Model) statusLine() string {
	var parts []string
	if m.loading {
		parts = append(parts, "loading")
	} else if !m.refreshed.IsZero() {
		parts = append(parts, "refreshed "+m.refreshed.In(m.be.Zone()).Format("15:04"))
	}
	if m.stack.Current().Kind == nav.Companies && m.vp.Len() > 0 {
		if pos := m.companyPos(); pos > 0 {
			parts = append(parts, fmt.Sprintf("company %s of %s", thousands(pos), thousands(m.companies)))
			if ticker := m.stockUnderCursor(); ticker != "" {
				if q, ok := m.data.Quotes[ticker]; ok {
					day := ticker + " EOD " + q.Day
					if q.stale(m.be.Now()) {
						day += " (stale)"
					}
					parts = append(parts, day)
				}
			}
		} else {
			parts = append(parts, "enter opens the group")
		}
	}
	if m.stack.Current().Kind == nav.Label && m.progress.Total > 0 {
		parts = append(parts, progressText(m.progress))
	}
	if m.navFocus {
		parts = append(parts, "navigation")
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
			keys = b.Keys[0] + " to " + b.Keys[len(b.Keys)-1]
		}
		entries[i] = fit(fmt.Sprintf("%-14s %s", keys, b.Help), col-2)
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
		// Numbered: the digit keys open the stock's page.
		numbered := make([]string, len(a.Tickers))
		for i, t := range a.Tickers {
			numbered[i] = fmt.Sprintf("%d %s", i+1, t)
			if i == 8 { // only 1-9 have a key
				numbered = numbered[:9]
				break
			}
		}
		add("stocks: " + clean(strings.Join(numbered, "  ")))
	}
	if len(a.Topics) > 0 {
		add("topics: " + clean(strings.Join(a.Topics, " ")))
	}
	if a.Form != "" {
		add("form: " + clean(a.Form))
	}
	for i, it := range model.DescribeItems(a.Items) {
		label := ""
		if i == 0 {
			label = "items: "
		}
		for j, l := range wrap(clean(it), w-9) {
			if j > 0 {
				label = ""
			}
			add(fmt.Sprintf("%-8s%s", label, l))
		}
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
	out = append(out, "", styleDim.Render("  + keep · - drop · b open in browser · 1-9 open a stock · ← back"))
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

// spanned draws text in the row's style with the spans in styles of their own;
// lead is the two cells before the text (the mark column).
func spanned(base lipgloss.Style, lead, text string, spans []Span) string {
	runes := []rune(text)
	var b strings.Builder
	b.WriteString(base.Render(lead))
	at := 0
	for _, sp := range spans {
		from, to := min(max(sp.From, at), len(runes)), min(max(sp.To, 0), len(runes))
		if to <= from {
			continue
		}
		if from > at {
			b.WriteString(base.Render(string(runes[at:from])))
		}
		child := sp.Style.lip()
		child = child.Reverse(base.GetReverse()).Bold(base.GetBold() || child.GetBold())
		b.WriteString(child.Render(string(runes[from:to])))
		at = to
	}
	if at < len(runes) {
		b.WriteString(base.Render(string(runes[at:])))
	}
	return b.String()
}
