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
)

const (
	defaultDays    = 14
	defaultRows    = 10 // pages.Params.Limit
	defaultRefresh = time.Minute
	searchLimit    = 200
	loadTimeout    = 30 * time.Second
	chromeLines    = 4 // watch bar, breadcrumbs, filter line, status line
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
	sel     []int // indices of the selectable rows of scr
	cursor  int   // index into sel
	top     int   // first visible row
	cursors map[string]int

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
	sources  []SourceRow

	detail *model.Article
	mode   mode
	filter []rune
	target string // the ticker a tag or list edit is for
	status string
	failed bool // status is an error

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
		seq       int
		arts      []model.Article
		sources   []SourceRow
		verdicts  map[string]labels.Verdict
		shared    bool
		truncated bool
		err       error
	}
	tickMsg  time.Time
	savedMsg struct {
		id   string
		keep bool
		err  error
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
		verdicts: map[string]labels.Verdict{},
	}
	watch := opts.Watch
	if watch == "" {
		watch = be.DefaultWatch()
	}
	m.stack.SwitchWatch(watch)
	m.rebuild()
	return m
}

// Init loads the watches and the first screen and starts the refresh timer.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.watchesCmd(), m.loadCmd(false), m.tickCmd())
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

// loadCmd fetches what the current screen is built from. With force false, a
// screen built from the shared window that is already loaded for this watch
// needs no fetch.
func (m *Model) loadCmd(force bool) tea.Cmd {
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
		r.Arg = strings.TrimSpace(string(m.filter)) // the list narrows as you type
	}
	p := m.params()
	d := Data{
		Params: p, Watchlist: m.be.Watchlist(), Topics: m.be.TopicNames(),
		Name: m.be.CompanyName, Verdicts: m.verdicts, Sources: m.sources,
		Watches: m.watches, WatchID: m.stack.Watch(), Now: m.be.Now(), Truncated: m.truncated,
		Universe: m.be.Universe(), Following: m.be.Following,
	}
	if needsShared(r.Kind) {
		d.Articles = m.shared
	} else {
		d.Articles = m.routeArts
	}
	m.data = d
	m.scr = Build(r, d)
	m.sel = m.sel[:0]
	for i, row := range m.scr.Rows {
		if row.Selectable() {
			m.sel = append(m.sel, i)
		}
	}
	m.cursor = min(m.cursors[r.String()], max(len(m.sel)-1, 0))
	if m.mode == modeFilter && r.Kind == nav.Companies {
		m.cursor = 0 // a query being typed starts from the best match
	}
	m.top = 0
	m.follow()
}
