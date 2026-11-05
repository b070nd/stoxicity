package tui

import (
	"fmt"
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.follow()
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case watchesMsg:
		if msg.err != nil {
			m.fail("watches: %v", msg.err)
			break
		}
		m.watches = msg.list
		m.checkWatch()
		m.rebuild()
	case loadedMsg:
		return m, m.loaded(msg)
	case tickMsg:
		return m, tea.Batch(m.tickCmd(), m.watchesCmd(), m.loadCmd(true))
	case savedMsg:
		if msg.err != nil {
			m.fail("label: %v", msg.err)
			break
		}
		v := labels.Drop
		word := "dropped"
		if msg.keep {
			v, word = labels.Keep, "kept"
		}
		m.verdicts[msg.id] = v
		m.ok("%s", word)
		m.rebuild()
	case openedMsg:
		if msg.err != nil {
			m.fail("open: %v", msg.err)
		}
	case seenMsg:
		return m, m.watchesCmd()
	case editedMsg:
		if msg.err != nil {
			m.fail("%v", msg.err)
			break
		}
		m.ok("%s", msg.what)
		m.rebuild()
	}
	return m, nil
}

func (m *Model) ok(format string, args ...any) {
	m.status, m.failed = fmt.Sprintf(format, args...), false
}

func (m *Model) fail(format string, args ...any) {
	m.status, m.failed = fmt.Sprintf(format, args...), true
}

// checkWatch falls back to all news when the current watch no longer exists.
func (m *Model) checkWatch() {
	cur := m.stack.Watch()
	if cur == "" {
		return
	}
	for _, w := range m.watches {
		if w.ID == cur {
			return
		}
	}
	m.stack.SwitchWatch("")
	m.sharedOK = false
	m.fail("watch %q not found; showing all news", cur)
}

// loaded takes the result of a load.
func (m *Model) loaded(msg loadedMsg) tea.Cmd {
	if msg.seq != m.seq {
		return nil // the user moved on
	}
	m.loading = false
	if msg.err != nil {
		m.fail("%v", msg.err)
		m.scr = Screen{Title: m.stack.Current().Label(), Notice: "could not load: " + msg.err.Error()}
		m.sel, m.cursor, m.top = nil, 0, 0
		return nil
	}
	m.verdicts = msg.verdicts
	switch {
	case msg.shared:
		m.shared, m.sharedWatch, m.sharedOK = msg.arts, m.stack.Watch(), true
	case m.stack.Current().Kind == nav.Sources:
		m.sources = msg.sources
	case m.stack.Current().Kind != nav.Watches:
		m.routeArts, m.truncated = msg.arts, msg.truncated
	}
	m.refreshed = m.be.Now()
	if !m.failed || !strings.HasPrefix(m.status, "watch ") {
		m.status, m.failed = "", false
	}
	m.rebuild()
	if w := m.stack.Watch(); w != "" && m.stack.Current().Scoped() {
		be, ctx := m.be, m.ctx
		return func() tea.Msg {
			if err := be.MarkSeen(ctx, w); err != nil {
				return savedMsg{err: err}
			}
			return seenMsg{}
		}
	}
	return nil
}

// ids is the watch IDs in tab order: all news first, then the watches.
func (m *Model) ids() []string {
	ids := make([]string, 0, len(m.watches)+1)
	ids = append(ids, "")
	for _, w := range m.watches {
		ids = append(ids, w.ID)
	}
	return ids
}

func (m *Model) switchWatch(id string) tea.Cmd {
	if id == m.stack.Watch() {
		return nil
	}
	m.stack.SwitchWatch(id)
	m.sharedOK = false
	m.detail = nil
	m.ok("watch: %s", m.watchName(id))
	m.showLoading()
	return m.loadCmd(true)
}

func (m *Model) watchName(id string) string {
	if id == "" {
		return "all news"
	}
	for _, w := range m.watches {
		if w.ID == id {
			return w.Name
		}
	}
	return id
}

// go_ opens a route.
func (m *Model) goTo(r nav.Route) tea.Cmd {
	m.cursors[m.stack.Current().String()] = m.cursor
	m.stack.Go(r)
	return m.entered()
}

// entered shows the screen the stack now points at.
func (m *Model) entered() tea.Cmd {
	m.detail = nil
	m.mode = modeList
	m.routeArts, m.truncated = nil, false
	r := m.stack.Current()
	if r.Kind == nav.Watches {
		m.rebuild()
		return m.watchesCmd()
	}
	cmd := m.loadCmd(false)
	if cmd != nil {
		m.showLoading()
	}
	return cmd
}

// showLoading replaces the screen with a placeholder until the load answers.
func (m *Model) showLoading() {
	m.scr = Screen{Title: m.stack.Current().Label(), Notice: "loading…"}
	m.sel, m.cursor, m.top = nil, 0, 0
}

// key handles a key press.
func (m *Model) key(k tea.KeyPressMsg) tea.Cmd {
	switch m.mode {
	case modeFilter:
		return m.filterKey(k)
	case modeTag, modeMember:
		return m.editKey(k)
	case modeHelp:
		m.mode = modeList
		return nil
	}
	b, ok := Lookup(k.String())
	if !ok {
		return nil
	}
	if m.detail != nil {
		return m.detailKey(b)
	}
	cur := m.stack.Current()
	switch b.Action {
	case Quit:
		m.quitting = true
		return tea.Quit
	case Help:
		m.mode = modeHelp
	case NextWatch:
		return m.switchWatch(nav.Next(m.ids(), m.stack.Watch(), 1))
	case PrevWatch:
		return m.switchWatch(nav.Next(m.ids(), m.stack.Watch(), -1))
	case JumpWatch:
		n := int(k.String()[0] - '0')
		ids := m.ids()[1:]
		id, ok := nav.Nth(ids, n)
		if !ok {
			m.fail("no watch %d", n)
			return nil
		}
		return m.switchWatch(id)
	case SwitchWatch, GoWatches:
		return m.goTo(nav.Route{Kind: nav.Watches})
	case SwitchTenant:
		m.fail("restart with -tenant NAME to switch tenant")
	case NewWatch, EditWatch, MuteWatch:
		m.fail("watches are YAML files in tenants/<tenant>/watches; `stoxicity watch check` validates them")
	case GoHome:
		return m.goTo(nav.Route{Kind: nav.Home})
	case GoStocks:
		return m.goTo(nav.Route{Kind: nav.Stocks})
	case GoCompanies:
		return m.goTo(nav.Route{Kind: nav.Companies})
	case TagStock:
		return m.startEdit(modeTag)
	case ListStock:
		return m.startEdit(modeMember)
	case GoTopics:
		return m.goTo(nav.Route{Kind: nav.Topics})
	case GoDomains:
		return m.goTo(nav.Route{Kind: nav.Domains})
	case GoSources:
		return m.goTo(nav.Route{Kind: nav.Sources})
	case Back:
		m.cursors[cur.String()] = m.cursor
		if m.stack.Back() {
			return m.entered()
		}
	case Forward:
		if m.stack.Forward() {
			return m.entered()
		}
	case Open:
		return m.openRow()
	case OpenURL:
		return m.openURL()
	case Up:
		m.move(-1)
	case Down:
		m.move(1)
	case PageUp:
		m.move(-m.bodyHeight())
	case PageDown:
		m.move(m.bodyHeight())
	case Top:
		m.move(-len(m.sel))
	case Bottom:
		m.move(len(m.sel))
	case FocusFilter:
		m.mode, m.filter = modeFilter, nil
		if cur.Kind == nav.Companies { // refine the query on screen
			m.filter = []rune(cur.Arg)
			if cur.Arg != "" {
				m.filter = append(m.filter, ' ')
			}
		}
	case Keep:
		return m.label(true)
	case Drop:
		return m.label(false)
	case Refresh:
		m.ok("refreshing")
		return tea.Batch(m.watchesCmd(), m.loadCmd(true))
	}
	return nil
}

// detailKey handles a key while an article is open.
func (m *Model) detailKey(b Binding) tea.Cmd {
	switch b.Action {
	case Quit:
		m.quitting = true
		return tea.Quit
	case Back, Open:
		m.detail = nil
	case OpenURL:
		return m.openURL()
	case Keep:
		return m.label(true)
	case Drop:
		return m.label(false)
	case Help:
		m.mode = modeHelp
	}
	return nil
}

// filterKey edits the filter bar. On the Companies screen the bar narrows the
// company list as you type and enter keeps the query as the route; elsewhere
// enter runs the query as a search.
func (m *Model) filterKey(k tea.KeyPressMsg) tea.Cmd {
	companies := m.stack.Current().Kind == nav.Companies
	switch k.String() {
	case "esc", "ctrl+c":
		m.mode = modeList
		if companies {
			m.rebuild()
		}
	case "enter":
		q := strings.TrimSpace(string(m.filter))
		m.mode = modeList
		if companies {
			if _, err := universe.ParseQuery(q); err != nil {
				m.fail("%v", err)
				m.mode = modeFilter
				return nil
			}
			if q == m.stack.Current().Arg {
				m.rebuild()
				return nil
			}
			return m.goTo(nav.Route{Kind: nav.Companies, Arg: q})
		}
		if q == "" {
			return nil
		}
		if _, err := filter.ParseWith(q, m.be.Universe().FilterEnv()); err != nil {
			m.fail("%v", err)
			m.mode = modeFilter
			return nil
		}
		return m.goTo(nav.Route{Kind: nav.Search, Arg: q})
	case "backspace":
		if n := len(m.filter); n > 0 {
			m.filter = m.filter[:n-1]
		}
	case "ctrl+u":
		m.filter = nil
	default:
		if t := k.Key().Text; t != "" {
			m.filter = append(m.filter, []rune(t)...)
		}
	}
	if companies && m.mode == modeFilter {
		m.rebuild()
	}
	return nil
}

// stockUnderCursor is the ticker a tag or list edit applies to: the stock
// whose page is open, or the stock of the selected row.
func (m *Model) stockUnderCursor() string {
	if r := m.stack.Current(); r.Kind == nav.Stock {
		return strings.ToUpper(r.Arg)
	}
	if row, ok := m.row(); ok && row.Go != nil && row.Go.Kind == nav.Stock {
		return strings.ToUpper(row.Go.Arg)
	}
	return ""
}

// startEdit opens the prompt for tags or lists.
func (m *Model) startEdit(to mode) tea.Cmd {
	t := m.stockUnderCursor()
	if t == "" {
		m.fail("put the cursor on a stock first")
		return nil
	}
	m.target, m.filter, m.mode = t, nil, to
	return nil
}

// editKey edits the tag or list prompt. Enter applies every word: a word adds
// the tag or list, a word with a leading "-" removes it.
func (m *Model) editKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc", "ctrl+c":
		m.mode = modeList
	case "enter":
		words := strings.Fields(strings.ToLower(strings.ReplaceAll(string(m.filter), "#", "")))
		to := m.mode
		m.mode = modeList
		if len(words) == 0 {
			return nil
		}
		return m.edit(to, m.target, words)
	case "backspace":
		if n := len(m.filter); n > 0 {
			m.filter = m.filter[:n-1]
		}
	case "ctrl+u":
		m.filter = nil
	default:
		if t := k.Key().Text; t != "" {
			m.filter = append(m.filter, []rune(t)...)
		}
	}
	return nil
}

// edit applies the words to the stock through the backend, one by one, and
// stops at the first failure.
func (m *Model) edit(to mode, ticker string, words []string) tea.Cmd {
	be, ctx := m.be, m.ctx
	noun := "tag"
	if to == modeMember {
		noun = "list"
	}
	return func() tea.Msg {
		var done []string
		for _, w := range words {
			add := !strings.HasPrefix(w, "-")
			w = strings.TrimPrefix(w, "-")
			var err error
			if to == modeMember {
				err = be.SetListMember(ctx, w, ticker, add)
			} else {
				err = be.Tag(ctx, ticker, w, add)
			}
			if err != nil {
				return editedMsg{err: fmt.Errorf("%s %s: %w", noun, w, err)}
			}
			if add {
				done = append(done, "+"+w)
			} else {
				done = append(done, "-"+w)
			}
		}
		return editedMsg{what: fmt.Sprintf("%s %ss %s", ticker, noun, strings.Join(done, " "))}
	}
}

// selected is the article under the cursor, or nil.
func (m *Model) selected() *model.Article {
	if m.detail != nil {
		return m.detail
	}
	if row, ok := m.row(); ok {
		return row.Article
	}
	return nil
}

func (m *Model) row() (Row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.sel) {
		return Row{}, false
	}
	return m.scr.Rows[m.sel[m.cursor]], true
}

// openRow follows the selected row.
func (m *Model) openRow() tea.Cmd {
	row, ok := m.row()
	if !ok {
		return nil
	}
	switch {
	case row.Article != nil:
		m.detail = row.Article
	case row.Go != nil:
		return m.goTo(*row.Go)
	case row.HasWatch:
		m.cursors[m.stack.Current().String()] = m.cursor
		m.stack.Go(nav.Route{Kind: nav.Home})
		m.stack.SwitchWatch(row.Watch)
		m.sharedOK = false
		m.ok("watch: %s", m.watchName(row.Watch))
		return m.entered()
	}
	return nil
}

// label records a verdict on the selected article.
func (m *Model) label(keep bool) tea.Cmd {
	a := m.selected()
	if a == nil {
		return nil
	}
	be, ctx, id := m.be, m.ctx, a.ID
	return func() tea.Msg {
		return savedMsg{id: id, keep: keep, err: be.Label(ctx, id, keep)}
	}
}

// link is the web address under the cursor: the open article's, the selected
// article's, or a profile row's website or filing page.
func (m *Model) link() (string, bool) {
	if a := m.selected(); a != nil {
		return a.URL, true
	}
	if row, ok := m.row(); ok && row.URL != "" {
		return row.URL, true
	}
	return "", false
}

// openURL opens the link under the cursor in the browser. Only web addresses
// are opened: feeds are untrusted, and a file: or custom-scheme link should not
// reach the system's handler.
func (m *Model) openURL() tea.Cmd {
	raw, ok := m.link()
	if !ok {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		m.fail("not a web address: %q", clipURL(raw))
		return nil
	}
	open, link := m.opts.OpenURL, u.String()
	return func() tea.Msg { return openedMsg{err: open(link)} }
}

func clipURL(s string) string { return clip(oneLine(s), 60) }

// move moves the cursor by n selectable rows.
func (m *Model) move(n int) {
	if len(m.sel) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+n, 0), len(m.sel)-1)
	m.follow()
}

// bodyHeight is how many rows the body shows.
func (m *Model) bodyHeight() int { return max(m.height-chromeLines, minHeight-chromeLines) }

// follow scrolls so that the cursor row is visible.
func (m *Model) follow() {
	h := m.bodyHeight()
	at := 0
	if m.cursor < len(m.sel) {
		at = m.sel[m.cursor]
	}
	if len(m.sel) == 0 || m.cursor == 0 {
		at = 0 // the first row: show the heading above it
	}
	switch {
	case at < m.top:
		m.top = at
	case at >= m.top+h:
		m.top = at - h + 1
	}
	m.top = max(m.top, 0)
}
