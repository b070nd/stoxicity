package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/status"
	"github.com/b070nd/stoxicity/app/src/universe"
)

const (
	defaultDays    = 14
	defaultRows    = 10 // pages.Params.Limit
	defaultRefresh = time.Minute
	defaultRedraw  = time.Second
	freshFor       = 8 * time.Second // how long a row that came in or changed stands out
	searchLimit    = 200
	loadTimeout    = 30 * time.Second
	chromeLines    = 5 // watch bar, meters, breadcrumbs, key bar (or prompt), status line
	minWidth       = 40
	minHeight      = 8
)

type mode int

const (
	modeList mode = iota
	modeFilter
	modeHelp
	modeTag    // typing tags for a stock
	modeMember // typing list names for a stock
	modeJump   // typing the start of a ticker or name to jump to in the catalogue
)

// Model is the Bubble Tea model of the interface. All of its state is plain
// data, and everything it does outside itself goes through the Backend and the
// commands it returns, so a test drives it with messages and no terminal.
type Model struct {
	be   Backend
	opts Options
	ctx  context.Context

	stack   *nav.Stack
	watches []WatchInfo

	width, height int

	data    Data
	scr     Screen
	vp      Viewport // the window onto scr and the cursor in it
	cursors map[string]int
	// builtFor is the route scr was last built for; a rebuild for the same route
	// keeps the cursor on the same row, a rebuild for another starts afresh.
	builtFor string
	// sorts is the order the user chose for each kind of screen with a table.
	sorts map[nav.Kind]Sort
	// group is the grouping the user chose for the catalogue with x.
	group string
	// exchange is the exchange every screen is narrowed to (x); empty shows all.
	exchange string
	// chipFocus: the arrows are on the exchange chips (up from the first row),
	// on the chip numbered chip.
	chipFocus bool
	chip      int
	// scoped is the last narrowing of the news, kept while nothing it came from
	// changes.
	scoped scopedNews
	// metered is the same for the meters, which count the shared window while a
	// Day or a Search screen is built from something else.
	metered scopedNews
	// latest is the newest news of each stock opened (any age), by ticker; the
	// stock page lists it. latestOK says it was asked for.
	latest map[string][]model.Article
	// marks are the tickers marked with space.
	marks map[string]bool
	// diffNext: the next rebuild follows a reload, so the rows that are new or
	// changed since the last build are noted in fresh (identity to when it
	// stops standing out).
	diffNext bool
	fresh    map[string]time.Time
	// counts are what the meters say of the news window.
	counts newsCounts
	// companies is how many stock rows the catalogue holds (its headings are
	// rows the cursor can stop on too, but are not companies).
	companies int
	// haveSources: sources has been loaded at least once.
	haveSources bool

	loading bool
	seq     int

	// shared is the article window the list screens are built from, and the
	// watch it was loaded for.
	shared      []model.Article
	sharedWatch string
	sharedOK    bool
	// routeArts is what a Day or Search screen is built from.
	routeArts []model.Article
	truncated bool

	verdicts map[string]labels.Verdict
	sources  []status.Source

	detail  *model.Article
	mode    mode
	prompt  Prompt   // the line being typed in the filter, tag, list and jump modes
	target  string   // what a tag or list edit is for, as the prompt names it
	editing []string // the tickers it applies to
	status  string
	failed  bool // status is an error

	refreshed time.Time
	quitting  bool
}

// Messages. Each result carries the sequence number of the load that asked
// for it, so an answer that arrives after the user moved on is dropped.
type (
	watchesMsg struct {
		list []WatchInfo
		err  error
	}
	loadedMsg struct {
		seq     int
		arts    []model.Article
		sources []status.Source
		// haveSources: sources was read (it may be empty).
		haveSources bool
		verdicts    map[string]labels.Verdict
		shared      bool
		truncated   bool
		err         error
	}
	tickMsg  time.Time
	clockMsg time.Time // the screen is drawn again
	savedMsg struct {
		id   string
		keep bool
		err  error
	}
	// latestMsg is the newest news of one stock.
	latestMsg struct {
		ticker string
		watch  string
		arts   []model.Article
		err    error
	}
	openedMsg struct{ err error }
	seenMsg   struct{}
	editedMsg struct { // a tag or list change finished
		what string
		err  error
	}
)

// New returns the model for a session. Options.Backend must be set.
func New(ctx context.Context, opts Options) *Model {
	opts = opts.withDefaults()
	be := opts.Backend
	m := &Model{
		be: be, opts: opts, ctx: ctx, stack: nav.New(),
		width: 100, height: 30, cursors: map[string]int{},
		sorts: map[nav.Kind]Sort{}, marks: map[string]bool{}, fresh: map[string]time.Time{},
		verdicts: map[string]labels.Verdict{}, latest: map[string][]model.Article{},
	}
	watch := opts.Watch
	if watch == "" {
		watch = be.DefaultWatch()
	}
	m.stack.SwitchWatch(watch)
	m.vp.Resize(m.bodyHeight() - 1)
	m.rebuild()
	return m
}

// Init loads the watches and the first screen and starts the refresh timer.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.watchesCmd(), m.loadCmd(false), m.tickCmd(), m.clockCmd())
}

func (m *Model) clockCmd() tea.Cmd {
	return tea.Tick(m.opts.Redraw, func(t time.Time) tea.Msg { return clockMsg(t) })
}

func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(m.opts.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) watchesCmd() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, loadTimeout)
		defer cancel()
		list, err := be.Watches(c)
		return watchesMsg{list: list, err: err}
	}
}

// params is the window every list screen covers.
func (m *Model) params() pages.Params {
	return pages.NewParams(m.be.Now(), m.opts.Days, defaultRows, m.be.Threshold(), m.be.Zone())
}

// needsShared reports whether the screen is built from the shared window.
func needsShared(k nav.Kind) bool {
	switch k {
	case nav.Day, nav.Search, nav.Sources, nav.Watches:
		return false
	}
	return true
}

// loadCmd fetches what the current screen is built from, and for a stock the
// newest news about it. See loadWindow.
func (m *Model) loadCmd(force bool) tea.Cmd {
	return tea.Batch(m.loadWindow(force), m.latestCmd())
}

// latestLimit is how many articles the stock page asks for, newest first.
const latestLimit = 40

// latestCmd asks for the newest news about the stock whose page is open, of any
// age: the window of the other screens can be empty for a stock that has been
// quiet for a month. It is nil on every other screen.
func (m *Model) latestCmd() tea.Cmd {
	r := m.stack.Current()
	if r.Kind != nav.Stock {
		return nil
	}
	ticker, watch, be, ctx := strings.ToUpper(r.Arg), m.stack.Watch(), m.be, m.ctx
	terms := []string{"ticker:" + ticker}
	if p, ok := be.Universe().Get(ticker); ok {
		for _, sib := range p.Siblings { // the other tickers of the same company
			terms = append(terms, "ticker:"+sib)
		}
	}
	query := terms[0]
	if len(terms) > 1 {
		query = "(" + strings.Join(terms, " OR ") + ")"
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, loadTimeout)
		defer cancel()
		arts, err := be.Search(c, watch, query, time.Time{}, time.Time{}, latestLimit)
		return latestMsg{ticker: ticker, watch: watch, arts: arts, err: err}
	}
}

// loadWindow fetches what the current screen is built from. With force false, a
// screen built from the shared window that is already loaded for this watch
// needs no fetch.
func (m *Model) loadWindow(force bool) tea.Cmd {
	r := m.stack.Current()
	watch := m.stack.Watch()
	if needsShared(r.Kind) && !force && m.sharedOK && m.sharedWatch == watch {
		m.rebuild()
		return nil
	}
	m.seq++
	seq, be, ctx := m.seq, m.be, m.ctx
	p := m.params()
	zone := be.Zone()
	m.loading = true
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, loadTimeout)
		defer cancel()
		out := loadedMsg{seq: seq}
		var err error
		switch r.Kind {
		case nav.Sources:
			out.sources, err = be.Sources(c)
		case nav.Watches:
			// The watch list is refreshed separately; nothing else to load.
		case nav.Day:
			var day time.Time
			day, err = time.ParseInLocation(time.DateOnly, r.Arg, zone)
			if err == nil {
				out.arts, err = be.Articles(c, watch, day, day.AddDate(0, 0, 1))
			}
		case nav.Search:
			out.arts, err = be.Search(c, watch, r.Arg, time.Time{}, time.Time{}, searchLimit)
			out.truncated = len(out.arts) >= searchLimit
		default:
			out.shared = true
			out.arts, err = be.Articles(c, watch, p.Previous().First, p.End())
		}
		if r.Kind != nav.Sources && err == nil {
			// The meters above every screen say how the sources are doing; a
			// source list that cannot be read must not spoil the screen.
			if src, serr := be.Sources(c); serr == nil {
				out.sources, out.haveSources = src, true
			}
		}
		if err == nil {
			out.verdicts, err = be.Verdicts(c)
		}
		out.err = err
		return out
	}
}

// rebuild builds the screen of the current route from what is loaded.
func (m *Model) rebuild() {
	r := m.stack.Current()
	if r.Kind == nav.Companies && m.mode == modeFilter {
		r.Arg = strings.TrimSpace(m.prompt.Text()) // the list narrows as you type
	}
	p := m.params()
	filtering := r.Kind == nav.Companies && m.mode == modeFilter
	same := r.String() == m.builtFor && !filtering
	key, near := "", m.vp.Cursor()
	if row, ok := m.row(); ok && same {
		key = row.Identity()
	}
	var before map[string]string
	if m.diffNext && same {
		// A screen with nothing on it (still loading, or empty) is no baseline.
		if before = m.texts(); len(before) == 0 {
			before = nil
		}
	}
	m.diffNext = false
	if !same {
		clear(m.fresh) // another screen: nothing on it is news yet
	}
	d := Data{
		Params: p, Watchlist: m.be.Watchlist(), Topics: m.be.TopicNames(),
		Name: m.be.CompanyName, Verdicts: m.verdicts, Sources: m.sources,
		Watches: m.watches, WatchID: m.stack.Watch(), Now: m.be.Now(), Truncated: m.truncated,
		Universe: m.be.Universe(), Following: m.be.Following, Feeds: m.be.StockFeeds, Quotes: m.be.Quotes(),
	}
	d.Exchange, d.Chosen = m.scopeOf(r), m.exchange
	if needsShared(r.Kind) {
		d.Articles = m.scoped.news(d.Universe, m.shared, d.Exchange)
	} else {
		d.Articles = m.scoped.news(d.Universe, m.routeArts, d.Exchange)
	}
	if r.Kind == nav.Stock {
		d.Latest, d.LatestOK = m.latest[strings.ToUpper(r.Arg)], m.latestKnown(r.Arg)
	}
	if !filtering { // the query being typed may say sort: or group: itself
		d.Sort = m.sorts[r.Kind]
		d.Group = m.group
	}
	m.data = d
	m.scr = Build(r, d)
	m.builtFor = r.String()
	if before != nil {
		m.noteFresh(before)
	}
	m.counts = m.countNews()
	m.companies = 0
	if r.Kind == nav.Companies {
		for _, row := range m.scr.Rows {
			if markable(row) != "" {
				m.companies++
			}
		}
	}
	switch {
	case filtering:
		m.vp.Reset(m.scr.Rows, 0) // a query being typed starts from the best match
		m.firstStock(r)
	case same:
		m.vp.Reload(m.scr.Rows, near)
		if i := m.find(key, near); i >= 0 {
			m.vp.Select(i)
		}
	default:
		at, seen := m.cursors[r.String()]
		m.vp.Reset(m.scr.Rows, at)
		if !seen {
			m.firstStock(r)
		}
	}
}

// firstStock puts the cursor of a catalogue on its first stock rather than on
// the heading of the first group, which is where the cursor starts otherwise.
func (m *Model) firstStock(r nav.Route) {
	if r.Kind != nav.Companies {
		return
	}
	for i := range m.vp.Len() {
		if markable(m.scr.Rows[m.vp.At(i)]) != "" {
			m.vp.Select(i)
			return
		}
	}
}

// find is the position, among the rows the cursor can stop on, of the row with
// this identity; of several, the one nearest to position near. -1 if there is
// none.
func (m *Model) find(key string, near int) int {
	if key == "" {
		return -1
	}
	best := -1
	for i := range m.vp.Len() {
		if m.scr.Rows[m.vp.At(i)].Identity() != key {
			continue
		}
		if best < 0 || abs(i-near) < abs(best-near) {
			best = i
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// newsCounts is what the meters say of the news: the articles in the window and
// how many of them are relevant.
type newsCounts struct {
	articles, relevant int
	ok                 bool // the window is loaded
}

func (m *Model) countNews() newsCounts {
	if !m.sharedOK {
		return m.counts
	}
	p := m.params()
	window := pages.InWindow(m.metered.news(m.be.Universe(), m.shared, m.exchange), p)
	return newsCounts{articles: len(window), relevant: len(pages.RelevantNewest(window, p.Threshold)), ok: true}
}

// texts is the text of every row the cursor can stop on, by identity.
func (m *Model) texts() map[string]string {
	out := make(map[string]string, m.vp.Len())
	for i := range m.vp.Len() {
		row := m.scr.Rows[m.vp.At(i)]
		out[row.Identity()] = row.Text
	}
	return out
}

// noteFresh finds the rows of the screen just built that were not in the
// screen before, or whose text is not what it was, and lets them stand out for
// a while. A re-sort changes no text, so it marks nothing; a reload that brings
// a new article, a source gone red or a mention counted marks exactly those.
func (m *Model) noteFresh(before map[string]string) {
	until := m.be.Now().Add(freshFor)
	n := 0
	for _, row := range m.scr.Rows {
		id := row.Identity()
		if id == "" || !row.Selectable() {
			continue
		}
		if old, ok := before[id]; !ok || old != row.Text {
			m.fresh[id] = until
			n++
		}
	}
	if n > 0 && !m.failed {
		m.ok("%d new or changed", n)
	}
}

// isFresh reports whether the row stands out now.
func (m *Model) isFresh(r Row) bool {
	until, ok := m.fresh[r.Identity()]
	return ok && m.be.Now().Before(until)
}

// companyPos is the place of the stock under the cursor among the stocks of the
// catalogue, counting from 1; 0 when the cursor is on a heading.
func (m *Model) companyPos() int {
	at, ok := m.vp.Row()
	if !ok || markable(m.scr.Rows[at]) == "" {
		return 0
	}
	n := 0
	for _, row := range m.scr.Rows[:at+1] {
		if markable(row) != "" {
			n++
		}
	}
	return n
}

// scopeOf is the exchange the screen of a route is narrowed to: the one chosen
// with X, except where that would defeat the screen. A stock is shown whatever
// its exchange; the list of exchanges shows them all; one exchange's page is
// about that exchange.
func (m *Model) scopeOf(r nav.Route) string {
	switch r.Kind {
	case nav.Stock, nav.Exchanges, nav.Sources, nav.Watches:
		return ""
	case nav.Exchange:
		return r.Arg
	}
	return m.exchange
}

// latestKnown reports whether the newest news of the stock was asked for.
func (m *Model) latestKnown(ticker string) bool {
	_, ok := m.latest[strings.ToUpper(ticker)]
	return ok
}

// scopedNews remembers the articles narrowed to an exchange, so that drawing
// again (every second) or typing in a filter does not narrow them again.
type scopedNews struct {
	exchange string
	univ     *universe.Universe
	src      *model.Article // the first article of what was narrowed
	n        int
	out      []model.Article
}

// news is arts narrowed to the exchange (all of them for none).
func (c *scopedNews) news(u *universe.Universe, arts []model.Article, exchange string) []model.Article {
	if exchange == "" || len(arts) == 0 {
		return arts
	}
	if c.exchange == exchange && c.univ == u && c.src == &arts[0] && c.n == len(arts) {
		return c.out
	}
	c.exchange, c.univ, c.src, c.n = exchange, u, &arts[0], len(arts)
	c.out = u.ScopeNews(arts, exchange)
	return c.out
}
