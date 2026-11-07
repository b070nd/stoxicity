package tui

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
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
		m.vp.Resize(m.bodyHeight() - 1)
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
	case latestMsg:
		if msg.watch != m.stack.Watch() {
			break // the user changed watch meanwhile
		}
		if msg.gen != m.latestAsked[msg.ticker] {
			break // a newer request for this stock is out: this answer is older than the screen
		}
		if msg.err != nil {
			m.fail("latest news of %s: %v", msg.ticker, msg.err)
			break
		}
		slices.SortStableFunc(msg.arts, func(a, b model.Article) int { return b.Published.Compare(a.Published) })
		m.latest[msg.ticker] = msg.arts
		m.rebuild()
	case tickMsg:
		return m, tea.Batch(m.tickCmd(), m.watchesCmd(), m.loadCmd(true))
	case clockMsg:
		return m, m.clockCmd() // drawing again is all it is for
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
		if m.stack.Current().Kind == nav.Label {
			return m, m.loadCmd(true) // the labels count, and the article leaves the queue
		}
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
	clear(m.latest)
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
		m.builtFor = ""
		m.vp.Clear()
		return nil
	}
	m.verdicts = msg.verdicts
	if m.stack.Current().Kind == nav.Label {
		m.queue, m.progress = msg.queue, msg.progress
	}
	if msg.sources != nil || msg.haveSources || m.stack.Current().Kind == nav.Sources {
		m.sources, m.haveSources = msg.sources, true
	}
	switch {
	case msg.shared:
		m.shared, m.sharedWatch, m.sharedOK = msg.arts, m.stack.Watch(), true
	case m.stack.Current().Kind == nav.Sources, m.stack.Current().Kind == nav.Label:
	case m.stack.Current().Kind != nav.Watches:
		m.routeArts, m.truncated = msg.arts, msg.truncated
	}
	m.refreshed = m.be.Now()
	if !m.failed || !strings.HasPrefix(m.status, "watch ") {
		m.status, m.failed = "", false
	}
	m.diffNext = true
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
	clear(m.latest) // what the watch lets through is another set
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
	m.cursors[m.stack.Current().String()] = m.vp.Cursor()
	m.stack.Go(r)
	return m.entered()
}

// entered shows the screen the stack now points at.
func (m *Model) entered() tea.Cmd {
	m.detail = nil
	m.chipFocus = false
	m.mode = modeList
	m.routeArts, m.truncated = nil, false
	r := m.stack.Current()
	if r.Kind == nav.Watches {
		m.rebuild()
		return m.watchesCmd()
	}
	cmd := m.loadWindow(false)
	if cmd != nil {
		m.showLoading()
	}
	return tea.Batch(cmd, m.latestCmd())
}

// showLoading replaces the screen with a placeholder until the load answers.
func (m *Model) showLoading() {
	m.scr = Screen{Title: m.stack.Current().Label(), Notice: "loading"}
	m.builtFor = ""
	m.vp.Clear()
}

// key handles a key press.
func (m *Model) key(k tea.KeyPressMsg) tea.Cmd {
	switch m.mode {
	case modeFilter, modeTag, modeMember, modeJump:
		return m.promptKey(k)
	case modeHelp:
		m.mode = modeList
		return nil
	}
	if m.chipFocus && m.detail == nil && m.chipKey(k.String()) {
		return nil
	}
	b, ok := Lookup(k.String())
	if !ok {
		return nil
	}
	c := m.context()
	if !b.Applies(c) {
		if !c.Detail { // in an open article the other keys are simply quiet
			m.fail("%s", b.Refusal(c))
		}
		return nil
	}
	if m.detail != nil {
		return m.detailKey(b, k)
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
	case GoWatches:
		return m.goTo(nav.Route{Kind: nav.Watches})
	case GoLabel:
		return m.goTo(nav.Route{Kind: nav.Label})
	case GoHome:
		return m.goTo(nav.Route{Kind: nav.Home})
	case GoStocks:
		// Every stock, those in the news first: the same list as the catalogue.
		return m.goTo(nav.Route{Kind: nav.Companies, Arg: stocksQuery})
	case GoCompanies:
		return m.goTo(nav.Route{Kind: nav.Companies})
	case TagStock:
		return m.startEdit(modeTag)
	case ListStock:
		return m.startEdit(modeMember)
	case GoTopics:
		return m.goTo(nav.Route{Kind: nav.Topics})
	case GoExchanges:
		return m.goTo(nav.Route{Kind: nav.Exchanges})
	case NextExchange:
		m.cycleExchange(1)
	case PrevExchange:
		m.cycleExchange(-1)
	case UseExchange:
		m.useExchange(cur)
	case GoDomains:
		return m.goTo(nav.Route{Kind: nav.Domains})
	case GoSources:
		return m.goTo(nav.Route{Kind: nav.Sources})
	case Back:
		m.cursors[cur.String()] = m.vp.Cursor()
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
		if m.vp.Cursor() == 0 && m.enterChips() { // up from the first row: the exchange filter
			return nil
		}
		m.vp.Move(-1)
	case Down:
		m.vp.Move(1)
	case PageUp:
		m.vp.Page(-1)
	case PageDown:
		m.vp.Page(1)
	case Top:
		m.vp.Home()
	case Bottom:
		m.vp.End()
	case SortNext:
		m.sortBy(1)
	case SortPrev:
		m.sortBy(-1)
	case SortInvert:
		m.sortBy(0)
	case GroupNext:
		keys := universe.GroupKeys()
		next := keys[(slices.Index(keys, m.scr.Group)+1)%len(keys)]
		m.group = next
		m.ok("grouped by %s", next)
		m.rebuild()
	case Mark:
		m.mark()
	case Unmark:
		clear(m.marks)
		m.ok("marks cleared")
	case FocusFilter:
		initial := ""
		if cur.Kind == nav.Companies { // refine the query on screen
			initial = cur.Arg
			if initial != "" {
				initial += " "
			}
		}
		m.mode = modeFilter
		m.prompt.Open("filter:"+string(cur.Kind), " / ", "", initial, false)
	case Jump:
		m.mode = modeJump
		m.prompt.Open("jump", " j jump to: ", "type the start of a ticker or name · esc ends", "", true)
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
func (m *Model) detailKey(b Binding, k tea.KeyPressMsg) tea.Cmd {
	switch b.Action {
	case Quit:
		m.quitting = true
		return tea.Quit
	case Back, Open:
		m.detail = nil
	case JumpWatch:
		return m.openTicker(int(k.String()[0] - '0'))
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

// promptKey passes a key to the open prompt and acts on what it came to. What
// the text means depends on which prompt it is.
func (m *Model) promptKey(k tea.KeyPressMsg) tea.Cmd {
	ev := m.prompt.Key(k)
	switch m.mode {
	case modeFilter:
		return m.filterEvent(ev)
	case modeJump:
		return m.jumpEvent(ev, k)
	}
	return m.editEvent(ev)
}

// filterEvent acts on the filter bar. On the Companies screen the bar narrows
// the company list as you type and enter keeps the query as the route;
// elsewhere enter runs the query as a search.
func (m *Model) filterEvent(ev PromptEvent) tea.Cmd {
	companies := m.stack.Current().Kind == nav.Companies
	switch ev {
	case Cancelled:
		m.mode = modeList
		if companies {
			m.rebuild()
		}
		return nil
	case Submitted:
		q := strings.TrimSpace(m.prompt.Text())
		if companies {
			if _, err := universe.ParseQuery(q); err != nil {
				m.fail("%v", err)
				return nil // the query stays in the bar to be mended
			}
			m.prompt.Remember()
			m.mode = modeList
			if q == m.stack.Current().Arg {
				m.rebuild()
				return nil
			}
			delete(m.sorts, nav.Companies) // the new query's own sort: and group: apply
			m.group = ""
			return m.goTo(nav.Route{Kind: nav.Companies, Arg: q})
		}
		if q == "" {
			m.mode = modeList
			return nil
		}
		if _, err := filter.ParseWith(q, m.be.Universe().FilterEnv()); err != nil {
			m.fail("%v", err)
			return nil
		}
		m.prompt.Remember()
		m.mode = modeList
		return m.goTo(nav.Route{Kind: nav.Search, Arg: q})
	}
	if companies {
		m.rebuild()
	}
	return nil
}

// jumpEvent acts on the jump prompt: what is typed is the start of a ticker (or
// of a name when the list is ordered by name), and the cursor goes to the first
// company that starts with it. The list is not filtered, so the neighbours stay
// in view. The movement keys end the jump and move the cursor.
func (m *Model) jumpEvent(ev PromptEvent, k tea.KeyPressMsg) tea.Cmd {
	switch ev {
	case Cancelled, Submitted:
		m.mode = modeList
		return nil
	case PassedOn:
		m.mode = modeList
		return m.key(k)
	}
	m.jumpTo(m.prompt.Text())
	return nil
}

// jumpTo puts the cursor on the first row of the catalogue whose ticker (or
// name, under sort:name) starts with prefix, and leaves it where it is when
// none does.
func (m *Model) jumpTo(prefix string) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" || m.data.Universe == nil {
		return
	}
	byName := m.scr.Sort.Key == "name"
	m.status, m.failed = "", false
	for i := range m.vp.Len() {
		row := m.scr.Rows[m.vp.At(i)]
		if row.Go == nil || row.Go.Kind != nav.Stock {
			continue
		}
		have := strings.ToLower(row.Go.Arg)
		if byName {
			if p, ok := m.data.Universe.Get(row.Go.Arg); ok {
				have = strings.ToLower(p.Name)
			}
		}
		if strings.HasPrefix(have, prefix) {
			m.vp.Select(i)
			return
		}
	}
	m.fail("nothing starts with %q", prefix)
}

// context is where a key is pressed now.
func (m *Model) context() Context {
	row, ok := m.row()
	return Context{
		Kind: m.stack.Current().Kind, Detail: m.detail != nil,
		Row: row, HasRow: ok, Table: m.scr.Table != nil, Article: m.selected() != nil, Marked: len(m.marks) > 0,
	}
}

// sortBy sorts the screen's table by the next (dir 1) or previous (-1) column,
// or turns the current order round (0). The cursor stays on its row.
func (m *Model) sortBy(dir int) {
	t := m.scr.Table
	if t == nil {
		m.fail("this screen has no columns to sort")
		return
	}
	var s Sort
	if dir == 0 {
		s = t.Invert(m.scr.Sort)
	} else {
		var ok bool
		if s, ok = t.Choose(m.scr.Sort, dir); !ok {
			return
		}
	}
	m.sorts[m.stack.Current().Kind] = s
	m.ok("sorted by %s", s)
	m.rebuild()
}

// markable is the ticker of the stock the row stands for, or "".
func markable(r Row) string {
	if r.Go != nil && r.Go.Kind == nav.Stock {
		return strings.ToUpper(r.Go.Arg)
	}
	return ""
}

// mark marks or unmarks the stock under the cursor and moves down, so that a
// run of stocks is marked by holding the key.
func (m *Model) mark() {
	row, ok := m.row()
	t := ""
	if ok {
		t = markable(row)
	}
	if t == "" {
		m.fail("put the cursor on a stock to mark it")
		return
	}
	if m.marks[t] {
		delete(m.marks, t)
	} else {
		m.marks[t] = true
	}
	m.vp.Move(1)
}

// targets are the stocks a tag or list edit applies to: every marked stock, or
// else the one under the cursor.
func (m *Model) targets() []string {
	if len(m.marks) > 0 {
		return slices.Sorted(maps.Keys(m.marks))
	}
	if t := m.stockUnderCursor(); t != "" {
		return []string{t}
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
	ts := m.targets()
	if len(ts) == 0 {
		m.fail("put the cursor on a stock first")
		return nil
	}
	m.editing, m.target, m.mode = ts, ts[0], to
	if len(ts) > 1 {
		m.target = fmt.Sprintf("%d marked stocks", len(ts))
	}
	if to == modeMember {
		m.prompt.Open("list", " L list for "+m.target+": ", "-name removes; a new name makes the list", "", false)
	} else {
		m.prompt.Open("tag", " # tag "+m.target+": ", "-name removes · enter saves · esc cancels", "", false)
	}
	return nil
}

// editEvent acts on the tag or list prompt. Enter applies every word: a word
// adds the tag or list, a word with a leading "-" removes it.
func (m *Model) editEvent(ev PromptEvent) tea.Cmd {
	switch ev {
	case Cancelled:
		m.mode = modeList
	case Submitted:
		words := strings.Fields(strings.ToLower(strings.ReplaceAll(m.prompt.Text(), "#", "")))
		to := m.mode
		m.mode = modeList
		if len(words) == 0 {
			return nil
		}
		m.prompt.Remember()
		return m.edit(to, m.editing, m.target, words)
	}
	return nil
}

// edit applies the words to the stock through the backend, one by one, and
// stops at the first failure.
func (m *Model) edit(to mode, tickers []string, label string, words []string) tea.Cmd {
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
			for _, ticker := range tickers {
				var err error
				if to == modeMember {
					err = be.SetListMember(ctx, w, ticker, add)
				} else {
					err = be.Tag(ctx, ticker, w, add)
				}
				if err != nil {
					return editedMsg{err: fmt.Errorf("%s %s %s: %w", ticker, noun, w, err)}
				}
			}
			if add {
				done = append(done, "+"+w)
			} else {
				done = append(done, "-"+w)
			}
		}
		return editedMsg{what: fmt.Sprintf("%s %ss %s", label, noun, strings.Join(done, " "))}
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
	at, ok := m.vp.Row()
	if !ok {
		return Row{}, false
	}
	return m.scr.Rows[at], true
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
		if row.Go.Kind == nav.Companies {
			m.group = "" // the group opened is the query's now, not x's
		}
		return m.goTo(*row.Go)
	case row.HasWatch:
		m.cursors[m.stack.Current().String()] = m.vp.Cursor()
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

func clipURL(s string) string { return oneLine(s) }

// bodyHeight is how many rows the body shows.
func (m *Model) bodyHeight() int { return max(m.height-chromeLines, minHeight-chromeLines) }

// setExchange narrows every screen to an exchange ("" for all of them).
func (m *Model) setExchange(name string) {
	m.exchange = name
	if name == "" {
		m.ok("exchange: all")
	} else {
		m.ok("exchange: %s (x for the next, ctrl+x for the previous)", name)
	}
	m.rebuild()
}

// cycleExchange steps the exchange all screens are narrowed to along the
// exchanges the companies are listed on, the biggest first, with "all" before
// the first and after the last.
func (m *Model) cycleExchange(dir int) {
	names := []string{""}
	for _, c := range m.be.Universe().Exchanges() {
		names = append(names, c.Name)
	}
	if len(names) == 1 {
		m.fail("no company has an exchange yet: run `stoxicity universe update`")
		return
	}
	i := slices.Index(names, m.exchange)
	if i < 0 {
		i = 0
	}
	m.setExchange(names[((i+dir)%len(names)+len(names))%len(names)])
}

// useExchange narrows every screen to the exchange the cursor is on in the list
// of exchanges, or the one whose page is open; asked again for the exchange that
// is already chosen, it shows all.
func (m *Model) useExchange(cur nav.Route) {
	name := ""
	switch cur.Kind {
	case nav.Exchange:
		name = cur.Arg
	case nav.Exchanges:
		if row, ok := m.row(); ok && row.Go != nil && row.Go.Kind == nav.Exchange {
			name = row.Go.Arg
		}
	}
	switch {
	case name == "":
		m.fail("put the cursor on an exchange first")
	case strings.EqualFold(name, m.exchange):
		m.setExchange("")
	default:
		m.setExchange(name)
	}
}

// openTicker leaves the open article for the page of its n-th stock.
func (m *Model) openTicker(n int) tea.Cmd {
	a := m.detail
	if a == nil || n < 1 || n > len(a.Tickers) {
		m.fail("this article names no stock %d", n)
		return nil
	}
	m.detail = nil
	return m.goTo(nav.Route{Kind: nav.Stock, Arg: a.Tickers[n-1]})
}
