package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/b070nd/stoxicity/app/src/status"
	"github.com/b070nd/stoxicity/app/src/universe"
)

var zone = func() *time.Location {
	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return time.UTC
	}
	return l
}()

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, zone)

// fake is a Backend over a fixed list of articles.
type fake struct {
	mu       sync.Mutex
	arts     []model.Article
	watches  []WatchInfo
	sources  []status.Source
	verdicts map[string]labels.Verdict

	layer universe.Layer
	univ  *universe.Universe

	articleCalls []string // the watch of each Articles call
	searchCalls  []string
	labelled     []string
	seen         []string
	err          error
}

func art(id, title, domain string, ago time.Duration, rel float64, mat int, tickers, topics []string) model.Article {
	return model.Article{
		ID: id, SourceID: "src", Domain: domain, URL: "https://" + domain + "/" + id, Title: title,
		Summary:   "Summary of " + title + ". It goes on for a while so that it can be wrapped.",
		Published: now.Add(-ago), Tickers: tickers, Topics: topics,
		Score: model.Score{Relevance: rel, Materiality: mat, Tone: 0.4, Kind: model.EventKind("earnings"), Reasons: []string{"matched earnings", "primary source"}},
	}
}

func newFake() *fake {
	f := newFakeArticles()
	f.rebuildUniverse()
	return f
}

// rebuildUniverse makes the universe from the fixed company list and the
// layer the tests edit.
func (f *fake) rebuildUniverse() {
	ents := []model.Entity{
		{Ticker: "KO", Name: "Coca-Cola Co", CIK: "0000021344", Exchange: "NYSE"},
		{Ticker: "NVDA", Name: "NVIDIA CORP", CIK: "0001045810", Exchange: "Nasdaq"},
		{Ticker: "GOOG", Name: "Alphabet Inc.", CIK: "0001652044", Exchange: "Nasdaq"},
		{Ticker: "GOOGL", Name: "Alphabet Inc.", CIK: "0001652044", Exchange: "Nasdaq"},
	}
	f.univ = universe.New(universe.Inputs{
		Master: company.Master{Entities: ents},
		Industry: company.IndustryCache{
			"0001045810": {CIK: "0001045810", SIC: "3674", Description: "Semiconductors & Related Devices", State: "DE", FiscalYear: "0126", Website: "www.nvidia.com"},
		},
		Layer: f.layer, Known: []string{"KO"},
	})
}

func (f *fake) Universe() *universe.Universe {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.univ
}

func (f *fake) Following(t string) []string {
	if t == "NVDA" {
		return []string{"Tech"}
	}
	return nil
}

func (f *fake) StockFeeds(t string) []FeedInfo {
	if t != "NVDA" {
		return nil
	}
	return []FeedInfo{
		{Kind: "sec", Title: "SEC filings", URL: "https://data.sec.gov/submissions/CIK0001045810.json", LastOK: f.Now().Add(-3 * time.Hour), LastNew: 2},
		{Kind: "site", Title: "NVIDIA newsroom", URL: "https://nvidianews.nvidia.com/rss", Failures: 2, LastError: "timeout"},
	}
}

func (f *fake) Tag(_ context.Context, ticker, tag string, add bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if add {
		if _, err := f.layer.AddTag(ticker, tag); err != nil {
			return err
		}
	} else {
		f.layer.RemoveTag(ticker, tag)
	}
	f.rebuildUniverse()
	return nil
}

func (f *fake) SetListMember(_ context.Context, id, ticker string, add bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.layer.SetMember(id, ticker, add); err != nil {
		return err
	}
	f.rebuildUniverse()
	return nil
}

func newFakeArticles() *fake {
	return &fake{
		arts: []model.Article{
			art("a1", "Nvidia beats estimates", "sec.gov", 2*time.Hour, 0.9, 4, []string{"NVDA"}, []string{"ai-infra"}),
			art("a2", "Coca-Cola raises dividend", "example.com", 30*time.Hour, 0.8, 3, []string{"KO"}, []string{"dividends"}),
			art("a3", "Meme stock chatter", "noise.net", 5*time.Hour, 0.1, 1, nil, nil),
			art("a4", "Nvidia supply chain", "example.com", 50*time.Hour, 0.7, 3, []string{"NVDA"}, []string{"ai-infra"}),
		},
		watches: []WatchInfo{{ID: "tech", Name: "Tech", Unread: 2}, {ID: "buffett", Name: "Buffett core", Unread: 0}},
		sources: []status.Source{
			{ID: "good", Kind: "rss", Enabled: true, Level: status.Green},
			{ID: "bad", Kind: "rss", Enabled: true, Level: status.Red, LastError: "boom"},
		},
		verdicts: map[string]labels.Verdict{},
	}
}

func (f *fake) Tenant() string       { return "default" }
func (f *fake) Zone() *time.Location { return zone }
func (f *fake) Threshold() float64   { return 0.5 }
func (f *fake) Now() time.Time       { return now }
func (f *fake) DefaultWatch() string { return "" }
func (f *fake) Watchlist() []string  { return []string{"KO"} }
func (f *fake) TopicNames() []string { return []string{"ai-infra", "dividends"} }
func (f *fake) CompanyName(t string) string {
	return map[string]string{"NVDA": "Nvidia Corp", "KO": "Coca-Cola Co"}[t]
}

func (f *fake) Watches(context.Context) ([]WatchInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.watches), f.err
}

func (f *fake) scoped(watch string, arts []model.Article) []model.Article {
	var out []model.Article
	for _, a := range arts {
		if watch == "tech" && !slices.Contains(a.Tickers, "NVDA") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (f *fake) Articles(_ context.Context, watch string, from, to time.Time) ([]model.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.articleCalls = append(f.articleCalls, watch)
	var in []model.Article
	for _, a := range f.arts {
		if !a.Published.Before(from) && a.Published.Before(to) {
			in = append(in, a)
		}
	}
	return f.scoped(watch, in), f.err
}

func (f *fake) Search(_ context.Context, watch, query string, _, _ time.Time, _ int) ([]model.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchCalls = append(f.searchCalls, watch+"|"+query)
	q, err := filter.Parse(query)
	if err != nil {
		return nil, err
	}
	var out []model.Article
	for _, a := range f.scoped(watch, f.arts) {
		if q.Match(a) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fake) Sources(context.Context) ([]status.Source, error) { return f.sources, nil }

func (f *fake) Verdicts(context.Context) (map[string]labels.Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]labels.Verdict{}
	for k, v := range f.verdicts {
		out[k] = v
	}
	return out, nil
}

func (f *fake) Label(_ context.Context, id string, keep bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labelled = append(f.labelled, fmt.Sprintf("%s:%t", id, keep))
	return f.err
}

func (f *fake) MarkSeen(_ context.Context, watch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, watch)
	return nil
}

// harness drives a Model with messages. It runs the commands the model
// returns and feeds their results back, except the refresh timer.
type harness struct {
	t      *testing.T
	m      *Model
	be     *fake
	opened []string
	openOK error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, be: newFake()}
	h.m = New(context.Background(), Options{
		Backend: h.be, Refresh: time.Millisecond, Redraw: time.Millisecond,
		OpenURL: func(u string) error { h.opened = append(h.opened, u); return h.openOK },
	})
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.run(h.m.Init())
	return h
}

func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	case tickMsg, clockMsg:
		// The timers are not run in tests.
	case tea.QuitMsg:
	default:
		_, next := h.m.Update(msg)
		h.run(next)
	}
}

// press sends keys, each named as Key.String() writes it, and runs the commands.
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		_, cmd := h.m.Update(keyMsg(k))
		h.run(cmd)
	}
}

// openStocks opens the screen of the stocks in the news (the Home heading's
// target); the s key opens the full catalogue instead.
func (h *harness) openStocks() {
	h.t.Helper()
	h.run(h.m.goTo(nav.Route{Kind: nav.Stocks}))
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.press(string(r))
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(k)
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

func (h *harness) view() string { return h.m.render() }

func (h *harness) crumbs() string {
	var parts []string
	for _, c := range h.m.stack.Crumbs() {
		parts = append(parts, c.Label)
	}
	return strings.Join(parts, " > ")
}

func (h *harness) mustContain(want ...string) {
	h.t.Helper()
	v := h.view()
	for _, w := range want {
		if !strings.Contains(v, w) {
			h.t.Errorf("view lacks %q:\n%s", w, v)
		}
	}
}

func TestStartsOnHomeAndShowsTheDigest(t *testing.T) {
	h := newHarness(t)
	h.mustContain("All news", "Tech", "Buffett core", "Home", "Nvidia beats estimates", "Stocks ›", "Domains ›", "Timeline")
	if h.crumbs() != "Home" {
		t.Errorf("crumbs = %q", h.crumbs())
	}
}

func TestWatchBarShowsUnreadOfOtherWatches(t *testing.T) {
	h := newHarness(t)
	h.mustContain("1 Tech (2)", "2 Buffett core")
}

func TestTabSwitchesWatchAndKeepsTheScreen(t *testing.T) {
	h := newHarness(t)
	h.openStocks()
	if h.crumbs() != "Home > Stocks" {
		t.Fatalf("crumbs = %q", h.crumbs())
	}
	h.press("tab")
	if h.m.stack.Watch() != "tech" {
		t.Fatalf("watch = %q", h.m.stack.Watch())
	}
	if h.crumbs() != "Home > Stocks" {
		t.Errorf("tab changed the screen: %q", h.crumbs())
	}
	if last := h.be.articleCalls[len(h.be.articleCalls)-1]; last != "tech" {
		t.Errorf("last load was for watch %q", last)
	}
	v := h.view()
	if strings.Contains(v, " KO ") || !strings.Contains(v, "NVDA") {
		t.Errorf("stocks of the tech watch should list NVDA and not KO:\n%s", v)
	}
	h.press("shift+tab")
	if h.m.stack.Watch() != "" {
		t.Errorf("shift+tab did not return to all news: %q", h.m.stack.Watch())
	}
}

func TestDigitJumpsToTheNthWatch(t *testing.T) {
	h := newHarness(t)
	h.press("2")
	if h.m.stack.Watch() != "buffett" {
		t.Errorf("watch = %q", h.m.stack.Watch())
	}
	h.press("9")
	if h.m.stack.Watch() != "buffett" || !h.m.failed {
		t.Errorf("a digit past the last watch should be an error and change nothing: %q %q", h.m.stack.Watch(), h.m.status)
	}
}

func TestLoadingAWatchMarksItSeen(t *testing.T) {
	h := newHarness(t)
	h.press("1")
	if !slices.Contains(h.be.seen, "tech") {
		t.Errorf("seen = %v", h.be.seen)
	}
	if slices.Contains(h.be.seen, "") {
		t.Error("all news has no read marker")
	}
}

func TestStocksRowOpensTheStockPage(t *testing.T) {
	h := newHarness(t)
	h.openStocks()
	h.mustContain("Stocks", "NVDA", "Nvidia Corp", "★")
	h.press("enter")
	if h.crumbs() != "Home > Stocks > NVDA" {
		t.Fatalf("crumbs = %q", h.crumbs())
	}
	h.mustContain("Stock NVDA · Nvidia Corp", "Timeline", "Nvidia beats estimates")
	h.press("esc")
	if h.crumbs() != "Home > Stocks" {
		t.Errorf("esc: crumbs = %q", h.crumbs())
	}
}

func TestBackRestoresTheCursor(t *testing.T) {
	h := newHarness(t)
	h.press("s", "down", "enter")
	if h.m.stack.Current().Kind != nav.Stock {
		t.Fatalf("crumbs = %q", h.crumbs())
	}
	h.press("esc")
	if h.m.vp.Cursor() != 1 {
		t.Errorf("cursor after back = %d, want 1", h.m.vp.Cursor())
	}
}

func TestTopicsAndTopicPage(t *testing.T) {
	h := newHarness(t)
	h.press("t")
	h.mustContain("Topics", "ai-infra", "lift")
	h.press("enter")
	h.mustContain("Topic ai-infra", "Stocks ›")
}

func TestDomainPageShowsPrecisionAgainstLabels(t *testing.T) {
	h := newHarness(t)
	h.be.verdicts["a2"] = labels.Keep
	h.be.verdicts["a4"] = labels.Drop
	h.press("D")
	h.mustContain("Domains", "example.com")
	// example.com is second: sec.gov has the most relevant articles per ratio.
	h.m.goTo(nav.Route{Kind: nav.Domain, Arg: "example.com"})
	h.run(h.m.loadCmd(true))
	h.mustContain("Domain example.com", "1 kept, 1 dropped", "precision 50%")
}

func TestHomeTimelineRowOpensTheDay(t *testing.T) {
	h := newHarness(t)
	var idx = -1
	for i, r := range h.m.scr.Rows {
		if r.Go != nil && r.Go.Kind == nav.Day {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no day row on Home")
	}
	h.m.vp.Select(h.m.vp.IndexOf(idx))
	h.press("enter")
	if h.m.stack.Current().Kind != nav.Day {
		t.Fatalf("route = %v", h.m.stack.Current())
	}
	h.mustContain("Day 2026-11-04", "Nvidia beats estimates")
}

func TestFilterBarRunsASearch(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText("nvidia -supply")
	h.press("enter")
	if got := h.m.stack.Current(); got.Kind != nav.Search || got.Arg != "nvidia -supply" {
		t.Fatalf("route = %+v", got)
	}
	h.mustContain("Search: nvidia -supply", "Nvidia beats estimates")
	if strings.Contains(h.view(), "supply chain") {
		t.Error("the negated term should have removed the article")
	}
	if len(h.be.searchCalls) != 1 || h.be.searchCalls[0] != "|nvidia -supply" {
		t.Errorf("search calls = %v", h.be.searchCalls)
	}
}

func TestBadQueryStaysInTheFilterBar(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText(`"unclosed`)
	h.press("enter")
	if h.m.mode != modeFilter || !h.m.failed {
		t.Errorf("mode = %v, failed = %v, status = %q", h.m.mode, h.m.failed, h.m.status)
	}
	if h.m.stack.Current().Kind == nav.Search {
		t.Error("a query that does not parse must not open a search")
	}
	h.press("esc")
	if h.m.mode != modeList {
		t.Error("esc should leave the filter bar")
	}
}

func TestFilterBarEditing(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText("abc")
	h.press("backspace")
	if h.m.prompt.Text() != "ab" {
		t.Errorf("filter = %q", h.m.prompt.Text())
	}
	h.press("space")
	if h.m.prompt.Text() != "ab " {
		t.Errorf("filter = %q", h.m.prompt.Text())
	}
	// Keys that are commands elsewhere are text in the filter bar.
	h.typeText("qs")
	if h.m.prompt.Text() != "ab qs" || h.m.quitting {
		t.Errorf("filter = %q, quitting = %v", h.m.prompt.Text(), h.m.quitting)
	}
}

// selectArticle moves the cursor to the first article row.
func (h *harness) selectArticle(title string) {
	h.t.Helper()
	for i, r := range h.m.scr.Rows {
		if r.Article != nil && r.Article.Title == title {
			h.m.vp.Select(h.m.vp.IndexOf(i))
			return
		}
	}
	h.t.Fatalf("no article row %q", title)
}

func (h *harness) selectArticleID(id string) {
	h.t.Helper()
	for i, r := range h.m.scr.Rows {
		if r.Article != nil && r.Article.ID == id {
			h.m.vp.Select(h.m.vp.IndexOf(i))
			return
		}
	}
	h.t.Fatalf("no article row %q", id)
}

func TestArticleDetailShowsWhyAndLabels(t *testing.T) {
	h := newHarness(t)
	h.openStocks()
	h.press("enter") // NVDA
	h.selectArticle("Nvidia beats estimates")
	h.press("enter")
	h.mustContain("Nvidia beats estimates", "Why this score", "matched earnings", "primary source", "your label: none")
	h.press("k")
	if got := h.be.labelled; len(got) != 1 || got[0] != "a1:true" {
		t.Fatalf("labelled = %v", got)
	}
	h.mustContain("your label: kept")
	h.press("d")
	if got := h.be.labelled; len(got) != 2 || got[1] != "a1:false" {
		t.Errorf("labelled = %v", got)
	}
	h.press("esc")
	if h.m.detail != nil {
		t.Error("esc should close the article")
	}
	if h.crumbs() != "Home > Stocks > NVDA" {
		t.Errorf("closing the article must not leave the screen: %q", h.crumbs())
	}
}

func TestLabelFromTheList(t *testing.T) {
	h := newHarness(t)
	h.selectArticle("Nvidia beats estimates")
	h.press("k")
	if len(h.be.labelled) != 1 {
		t.Fatalf("labelled = %v", h.be.labelled)
	}
	h.be.verdicts["a1"] = labels.Keep
	h.mustContain("+ ")
}

func TestLabelFailureIsReported(t *testing.T) {
	h := newHarness(t)
	h.selectArticle("Nvidia beats estimates")
	h.be.err = errors.New("disk full")
	h.press("k")
	if !h.m.failed || !strings.Contains(h.m.status, "disk full") {
		t.Errorf("status = %q", h.m.status)
	}
	if _, ok := h.m.verdicts["a1"]; ok {
		t.Error("a failed label must not show as saved")
	}
}

func TestOpenURLOpensWebAddressesOnly(t *testing.T) {
	h := newHarness(t)
	h.selectArticle("Nvidia beats estimates")
	h.press("o")
	if len(h.opened) != 1 || h.opened[0] != "https://sec.gov/a1" {
		t.Fatalf("opened = %v", h.opened)
	}

	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "ssh://host", "https:///nohost", "-flag", ""} {
		h.opened = nil
		h.m.failed = false
		for i := range h.m.scr.Rows {
			if a := h.m.scr.Rows[i].Article; a != nil && a.Title == "Nvidia beats estimates" {
				a.URL = bad
			}
		}
		h.press("o")
		if len(h.opened) != 0 {
			t.Errorf("opened %q", bad)
		}
		if !h.m.failed {
			t.Errorf("%q: no error shown", bad)
		}
	}
}

func TestOpenerFailureIsShown(t *testing.T) {
	h := newHarness(t)
	h.openOK = errors.New("no browser")
	h.selectArticle("Nvidia beats estimates")
	h.press("o")
	if !h.m.failed || !strings.Contains(h.m.status, "no browser") {
		t.Errorf("status = %q", h.m.status)
	}
}

func TestFeedTextCannotDrawEscapeSequences(t *testing.T) {
	h := newHarness(t)
	h.be.arts[0].Title = "Pwn\x1b]0;owned\x07ed\x1b[2J title"
	h.be.arts[0].Summary = "before \x1b[45mred\x1b[0m after"
	h.press("r")
	for _, screen := range []string{h.view()} {
		if strings.Contains(screen, "\x1b]") || strings.Contains(screen, "owned") || strings.Contains(screen, "\x1b[2J") {
			t.Errorf("an escape sequence from a feed reached the screen:\n%q", screen)
		}
	}
	h.selectArticleID("a1")
	h.press("enter")
	if v := h.view(); strings.Contains(v, "\x1b[45m") || strings.Contains(v, "\x1b]") {
		t.Errorf("detail view drew a feed escape sequence: %q", v)
	}
}

func TestSourcesScreenListsFailingFirst(t *testing.T) {
	h := newHarness(t)
	h.press("S")
	h.mustContain("Source health", "bad", "good", "boom")
	v := h.view()
	if strings.Index(v, "bad") > strings.Index(v, "good") {
		t.Errorf("a red source should come before a green one:\n%s", v)
	}
	if slices.Contains(h.be.seen, "") {
		t.Error("the sources screen belongs to no watch")
	}
}

func TestWatchesScreenSwitchesWatch(t *testing.T) {
	h := newHarness(t)
	h.press("W")
	h.mustContain("Watches", "All news", "Tech", "2 unread")
	h.press("down", "enter") // Tech
	if h.m.stack.Watch() != "tech" {
		t.Fatalf("watch = %q", h.m.stack.Watch())
	}
	if h.crumbs() != "Home" {
		t.Errorf("crumbs = %q", h.crumbs())
	}
}

func TestMissingWatchFallsBackToAllNews(t *testing.T) {
	be := newFake()
	m := New(context.Background(), Options{Backend: be, Watch: "gone", Refresh: time.Millisecond, Redraw: time.Millisecond})
	h := &harness{t: t, m: m, be: be}
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.run(m.Init())
	if m.stack.Watch() != "" {
		t.Errorf("watch = %q", m.stack.Watch())
	}
	if !m.failed || !strings.Contains(m.status, "gone") {
		t.Errorf("status = %q", m.status)
	}
}

func TestStaleLoadIsDropped(t *testing.T) {
	h := newHarness(t)
	before := h.m.scr.Title
	h.m.Update(loadedMsg{seq: h.m.seq - 1, arts: nil})
	if h.m.scr.Title != before || len(h.m.shared) == 0 {
		t.Error("an old answer replaced the current data")
	}
}

func TestLoadErrorShowsInTheBody(t *testing.T) {
	h := newHarness(t)
	h.be.err = errors.New("database is locked")
	h.press("r")
	h.mustContain("database is locked")
	h.be.err = nil
	h.press("r")
	if h.m.failed {
		t.Errorf("a good reload should clear the error: %q", h.m.status)
	}
	h.mustContain("Nvidia beats estimates")
}

func TestRefreshReloads(t *testing.T) {
	h := newHarness(t)
	n := len(h.be.articleCalls)
	h.press("r")
	if len(h.be.articleCalls) != n+1 {
		t.Errorf("calls %d -> %d", n, len(h.be.articleCalls))
	}
}

func TestNavigationBetweenListScreensDoesNotReload(t *testing.T) {
	h := newHarness(t)
	n := len(h.be.articleCalls)
	h.press("s", "t", "D", "H")
	if len(h.be.articleCalls) != n {
		t.Errorf("list screens share one window; got %d extra loads", len(h.be.articleCalls)-n)
	}
}

func TestEmptyBaseTellsWhatToDo(t *testing.T) {
	h := newHarness(t)
	h.be.arts = nil
	h.press("r")
	h.mustContain("stoxicity fetch")
}

func TestUnknownTopicSuggestsClosest(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Topic, Arg: "ai-infr"})
	h.mustContain("Did you mean")
}

func TestHelpListsEveryBinding(t *testing.T) {
	h := newHarness(t)
	h.press("?")
	v := h.view()
	for _, b := range DefaultKeys {
		if !strings.Contains(v, b.Help) {
			t.Errorf("help lacks %q", b.Help)
		}
	}
	h.press("x")
	if h.m.mode != modeList {
		t.Error("any key closes help")
	}
}

func TestWatchManagementPointsToTheCommand(t *testing.T) {
	h := newHarness(t)
	h.press("n")
	if !strings.Contains(h.m.status, "tenants/<tenant>/watches") {
		t.Errorf("status = %q", h.m.status)
	}
}

func TestQuit(t *testing.T) {
	h := newHarness(t)
	_, cmd := h.m.Update(keyMsg("q"))
	if cmd == nil || !h.m.quitting {
		t.Fatal("q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q should return the quit message")
	}
}

func TestTooSmallTerminal(t *testing.T) {
	h := newHarness(t)
	h.m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	h.mustContain("too small")
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.mustContain("Home")
}

func TestEveryLineFitsTheWidth(t *testing.T) {
	h := newHarness(t)
	for _, size := range [][2]int{{40, 10}, {60, 20}, {120, 40}} {
		h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, keys := range [][]string{{"H"}, {"s"}, {"t"}, {"D"}, {"S"}, {"W"}} {
			h.press(keys...)
			lines := strings.Split(h.view(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%v %v: %d lines, want %d", size, keys, len(lines), size[1])
			}
			for _, l := range lines {
				if w := lipglossWidth(l); w > size[0] {
					t.Errorf("%v %v: line of %d cells: %q", size, keys, w, l)
				}
			}
		}
	}
}

func TestCursorStaysVisibleWhileScrolling(t *testing.T) {
	h := newHarness(t)
	h.m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	for i := 0; i < 30; i++ {
		h.press("down")
		at := h.m.vp.At(h.m.vp.Cursor())
		if at < h.m.vp.Top() || at >= h.m.vp.Top()+h.m.bodyHeight()-1 {
			t.Fatalf("step %d: cursor row %d outside [%d, %d)", i, at, h.m.vp.Top(), h.m.vp.Top()+h.m.bodyHeight()-1)
		}
	}
	h.press("up")
	h.m.vp.Move(-100)
	if h.m.vp.Cursor() != 0 || h.m.vp.Top() != 0 {
		t.Errorf("cursor %d, top %d after going to the top", h.m.vp.Cursor(), h.m.vp.Top())
	}
}

func TestRunNeedsABackend(t *testing.T) {
	if err := Run(context.Background(), Options{}); err == nil {
		t.Error("Run without a backend should fail")
	}
}

func lipglossWidth(s string) int { return lipgloss.Width(s) }

func TestTickReloadsAndKeepsTheTimerRunning(t *testing.T) {
	h := newHarness(t)
	n := len(h.be.articleCalls)
	_, cmd := h.m.Update(tickMsg(now))
	h.run(cmd)
	if len(h.be.articleCalls) != n+1 {
		t.Errorf("a tick should reload: %d -> %d calls", n, len(h.be.articleCalls))
	}
	if cmd == nil {
		t.Error("a tick must schedule the next one")
	}
}

func TestBuildStockScreen(t *testing.T) {
	be := newFake()
	d := Data{
		Params: pagesParams(), Articles: be.arts, Watchlist: []string{"KO"}, Name: be.CompanyName,
		Verdicts: map[string]labels.Verdict{"a1": labels.Keep},
	}
	s := Build(nav.Route{Kind: nav.Stock, Arg: "nvda"}, d)
	if s.Title != "Stock NVDA · Nvidia Corp" {
		t.Errorf("title = %q", s.Title)
	}
	var articles, days int
	for _, r := range s.Rows {
		if r.Article != nil {
			articles++
		}
		if r.Go != nil && r.Go.Kind == nav.Day {
			days++
		}
	}
	if articles < 2 || days != 14 {
		t.Errorf("articles = %d, day rows = %d; want >= 2 and 14", articles, days)
	}
	if empty := Build(nav.Route{Kind: nav.Stock, Arg: "ZZZ"}, d); empty.Notice == "" || len(empty.Rows) != 0 {
		t.Errorf("an unknown stock should explain itself: %+v", empty)
	}
}

func TestBuildMarksLabelsAndDimsIrrelevantArticles(t *testing.T) {
	be := newFake()
	d := Data{Params: pagesParams(), Articles: be.arts, Verdicts: map[string]labels.Verdict{"a1": labels.Keep, "a3": labels.Drop}}
	s := Build(nav.Route{Kind: nav.Search, Arg: "x"}, Data{
		Params: d.Params, Articles: be.arts, Verdicts: d.Verdicts,
	})
	for _, r := range s.Rows {
		if r.Article == nil {
			continue
		}
		switch r.Article.ID {
		case "a1":
			if !strings.HasPrefix(r.Text, "+ ") || r.Style != Plain {
				t.Errorf("a1: %q style %v", r.Text, r.Style)
			}
		case "a3":
			if !strings.HasPrefix(r.Text, "- ") || r.Style != Dim {
				t.Errorf("a3: %q style %v", r.Text, r.Style)
			}
		}
	}
}

func TestBuildSourcesPutsFailuresFirstAndDisabledLast(t *testing.T) {
	s := Build(nav.Route{Kind: nav.Sources}, Data{Params: pagesParams(), Sources: []status.Source{
		{ID: "off", Enabled: false, State: status.Off, Level: status.Red},
		{ID: "ok", Enabled: true, Level: status.Green},
		{ID: "never", Enabled: true},
		{ID: "bad", Enabled: true, Level: status.Red},
		{ID: "slow", Enabled: true, Level: status.Yellow},
	}})
	var order []string
	for _, r := range s.Rows[1:] {
		order = append(order, strings.Fields(r.Text)[0])
	}
	if want := []string{"bad", "slow", "never", "ok", "off"}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestBuildDayWithoutArticles(t *testing.T) {
	s := Build(nav.Route{Kind: nav.Day, Arg: "2026-01-01"}, Data{Params: pagesParams()})
	if s.Notice == "" {
		t.Error("an empty day should say so")
	}
}

func TestBarShowsRelevantShareOfTotal(t *testing.T) {
	if got := bar(10, 5, 10, 20); got != strings.Repeat("█", 10)+strings.Repeat("░", 10) {
		t.Errorf("bar = %q", got)
	}
	if got := bar(1, 1, 100, 20); got != "█" {
		t.Errorf("a small count must still show: %q", got)
	}
	if bar(0, 0, 10, 20) != "" || bar(3, 1, 0, 20) != "" {
		t.Error("nothing to draw")
	}
}

func TestWrapBreaksLongWordsAndKeepsParagraphs(t *testing.T) {
	got := wrap("aaaa bbbb cccc\nxxxxxxxxxx", 9)
	want := []string{"aaaa bbbb", "cccc", "xxxxxxxxx", "x"}
	if !slices.Equal(got, want) {
		t.Errorf("wrap = %q, want %q", got, want)
	}
}

func pagesParams() pages.Params {
	return pages.NewParams(now, 14, 10, 0.5, zone)
}

func TestCompaniesScreenListsEveryCompanyWithCountsAndFacets(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.mustContain("Companies", "4 of 4 companies", "exchange: Nasdaq 3", "NVDA", "Nvidia Corp", "Coca-Cola", "★", "GOOGL", "NEWS")
	if got := h.crumbs(); !strings.HasSuffix(got, "Companies") {
		t.Errorf("crumbs = %q", got)
	}
}

func TestCompaniesNarrowAsYouTypeAndEnterKeepsTheQuery(t *testing.T) {
	h := newHarness(t)
	h.press("c", "/")
	h.typeText("exchange:nyse")
	v := h.view()
	if !strings.Contains(v, "Coca-Cola") || strings.Contains(v, "NVDA") {
		t.Fatalf("live narrowing did not apply:\n%s", v)
	}
	if got := h.m.stack.Current().Arg; got != "" {
		t.Errorf("route changed while typing: %q", got)
	}
	h.press("enter")
	if got := h.m.stack.Current(); got.Kind != nav.Companies || got.Arg != "exchange:nyse" {
		t.Errorf("route = %v", got)
	}
	h.mustContain("1 of 4 companies")
	// Esc leaves the committed query alone.
	h.press("/", "backspace", "esc")
	if h.m.stack.Current().Arg != "exchange:nyse" {
		t.Errorf("esc changed the route")
	}
	h.mustContain("1 of 4 companies")
}

func TestCompaniesBadQueryStaysInTheBarWithTheError(t *testing.T) {
	h := newHarness(t)
	h.press("c", "/")
	h.typeText("exchange:")
	h.mustContain("exchange")
	h.press("enter")
	if h.m.mode != modeFilter || !h.m.failed {
		t.Errorf("mode = %v failed = %v, want the bar kept open with an error", h.m.mode, h.m.failed)
	}
}

func TestSlashOutsideCompaniesStillSearchesWithUniverseOperators(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText("list:nope")
	h.press("enter")
	if !h.m.failed {
		t.Errorf("an unknown list should be reported, status %q", h.m.status)
	}
	h.press("esc")
}

func TestStockProfileShowsWhatIsKnownEvenWithoutNews(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "GOOG"})
	h.run(h.m.loadCmd(true))
	h.mustContain("Stock GOOG", "Profile", "Alphabet Inc.", "GOOGL", "No article names GOOG")
	h.mustContain("universe enrich GOOG")
}

func TestStockProfileShowsIndustryWatchesAndLinks(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "NVDA"})
	h.run(h.m.loadCmd(true))
	h.mustContain("Semiconductors & Related Devices (SIC 3674)", "Nasdaq", "watches Tech", "www.nvidia.com", "EDGAR", "Nvidia beats estimates")
}

func TestOpenKeyOnAProfileRowOpensWebsiteAndFilingPages(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "NVDA"})
	h.run(h.m.loadCmd(true))
	var got []string
	for i := range h.m.vp.Len() {
		if h.m.scr.Rows[h.m.vp.At(i)].URL != "" {
			h.m.vp.Select(i)
			h.press("o")
			got = append(got, h.opened[len(h.opened)-1])
		}
	}
	// The website, the filing page, then the two feeds the stock has: the SEC
	// record opens the same filing page, the company's feed opens the feed.
	if len(got) != 4 || got[0] != "https://www.nvidia.com" || !strings.Contains(got[1], "sec.gov/cgi-bin/browse-edgar") ||
		!strings.Contains(got[1], "CIK=0001045810") || got[2] != got[1] || got[3] != "https://nvidianews.nvidia.com/rss" {
		t.Errorf("opened %v", got)
	}
}

func TestStockProfileListsItsFeedsAndHowTheyAreDoing(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "NVDA"})
	h.run(h.m.loadCmd(true))
	h.mustContain("feeds", "SEC filings", "read 3h ago, 2 new", "company site", "failing (2×): timeout", "NVIDIA newsroom")
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "GOOG"})
	h.run(h.m.loadCmd(true))
	h.mustContain("none recorded yet")
}

func TestUnknownTickerWithNoNewsSaysSo(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "ZZZZ"})
	h.run(h.m.loadCmd(true))
	h.mustContain("no company list knows it")
}

func TestTagPromptAddsAndRemovesTags(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "NVDA"})
	h.run(h.m.loadCmd(true))
	h.press("#")
	if h.m.mode != modeTag {
		t.Fatalf("mode = %v, want the tag prompt", h.m.mode)
	}
	h.typeText("moat ai")
	h.press("enter")
	h.mustContain("#moat", "#ai", "NVDA tags +moat +ai")
	p, _ := h.be.Universe().Get("NVDA")
	if strings.Join(p.Tags, ",") != "ai,moat" {
		t.Errorf("tags = %v", p.Tags)
	}
	h.press("#")
	h.typeText("-moat")
	h.press("enter")
	if p, _ := h.be.Universe().Get("NVDA"); strings.Join(p.Tags, ",") != "ai" {
		t.Errorf("tags after removal = %v", p.Tags)
	}
	h.press("#")
	h.typeText("Bad Tag!")
	h.press("enter")
	if !h.m.failed {
		t.Errorf("an invalid tag should fail, status %q", h.m.status)
	}
}

func TestListPromptCreatesAListAndCompaniesCanBeFilteredByIt(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.press("g") // the catalogue is in the order of the tickers: jump to NVDA
	h.typeText("nv")
	h.press("enter")
	h.press("L")
	h.typeText("picks")
	h.press("enter")
	h.mustContain("NVDA lists +picks", "lists: picks 1")
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "list:picks"})
	h.run(h.m.loadCmd(false))
	h.mustContain("1 of 4 companies")
}

func TestTagPromptNeedsAStockUnderTheCursor(t *testing.T) {
	h := newHarness(t)
	h.press("t") // topics: no stock rows
	h.press("#")
	if h.m.mode == modeTag || !h.m.failed {
		t.Errorf("mode = %v failed = %v, want a refusal", h.m.mode, h.m.failed)
	}
}

func TestEscCancelsAnEditPrompt(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "NVDA"})
	h.run(h.m.loadCmd(true))
	h.press("#")
	h.typeText("x")
	h.press("esc")
	if h.m.mode != modeList {
		t.Errorf("mode = %v", h.m.mode)
	}
	if p, _ := h.be.Universe().Get("NVDA"); len(p.Tags) != 0 {
		t.Errorf("tags = %v", p.Tags)
	}
}

// bigCatalogue gives the fake backend n companies, T0000 to T<n-1>.
func (h *harness) bigCatalogue(n int) {
	ents := make([]model.Entity, n)
	for i := range ents {
		ents[i] = model.Entity{Ticker: fmt.Sprintf("T%04d", i), Name: fmt.Sprintf("Company %04d Inc", n-i), CIK: fmt.Sprintf("%010d", i+1), Exchange: "NYSE"}
	}
	h.be.mu.Lock()
	h.be.univ = universe.New(universe.Inputs{Master: company.Master{Entities: ents}})
	h.be.mu.Unlock()
}

func TestCatalogueListsEveryCompanyNotAShortlist(t *testing.T) {
	h := newHarness(t)
	h.bigCatalogue(1500)
	h.press("c")
	h.mustContain("1,500 of 1,500 companies", "by ticker", "TICKER", "T0000", "company 1 of 1,500")
	if h.m.companies != 1500 || h.m.vp.Len() != 1501 {
		t.Fatalf("%d companies in %d rows to stop on, want all 1500 under one heading", h.m.companies, h.m.vp.Len())
	}
	h.press("end")
	h.mustContain("T1499", "company 1,500 of 1,500")
	h.press("home")
	h.press("pgdown")
	if h.m.vp.Cursor() == 0 || h.m.vp.Cursor() >= 100 {
		t.Errorf("page down moved to %d, want about one screen", h.m.vp.Cursor())
	}
}

func TestCatalogueJumpGoesToTheFirstMatchAndKeepsTheList(t *testing.T) {
	h := newHarness(t)
	h.bigCatalogue(1500)
	h.press("c")
	h.press("g")
	if h.m.mode != modeJump {
		t.Fatalf("mode = %v, want jump", h.m.mode)
	}
	h.typeText("t09")
	h.mustContain("g jump to: t09", "T0900")
	if h.m.vp.Cursor() != 901 {
		t.Fatalf("cursor = %d, want 901", h.m.vp.Cursor())
	}
	h.typeText("5")
	if h.m.vp.Cursor() != 951 {
		t.Fatalf("cursor = %d after t095, want 951", h.m.vp.Cursor())
	}
	h.typeText("zz")
	if !h.m.failed || h.m.vp.Cursor() != 951 {
		t.Errorf("no match should say so and stay put: failed=%v cursor=%d", h.m.failed, h.m.vp.Cursor())
	}
	h.press("esc")
	if h.m.mode != modeList || h.m.companies != 1500 {
		t.Errorf("mode %v, %d rows: jumping must not filter", h.m.mode, h.m.vp.Len())
	}
	// The jump is for the catalogue only.
	h.press("t")
	h.press("g")
	if h.m.mode == modeJump || !h.m.failed {
		t.Errorf("g outside the catalogue should refuse")
	}
}

func TestCatalogueSortsByQuery(t *testing.T) {
	h := newHarness(t)
	h.bigCatalogue(300)
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "sort:-ticker group:none"})
	h.run(h.m.loadCmd(false))
	h.mustContain("by ticker, reversed")
	if got := h.m.scr.Rows[h.m.vp.At(0)].Go.Arg; got != "T0299" {
		t.Errorf("first row %s, want T0299", got)
	}
	// The names run backwards against the tickers; sort:name puts the last ticker first.
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "sort:name group:none"})
	h.run(h.m.loadCmd(false))
	h.mustContain("by name")
	if got := h.m.scr.Rows[h.m.vp.At(0)].Go.Arg; got != "T0299" {
		t.Errorf("first row by name %s, want T0299", got)
	}
	// Jumping follows the name under sort:name.
	h.press("g")
	h.typeText("company 0100")
	if got := h.m.scr.Rows[h.m.vp.At(h.m.vp.Cursor())].Go.Arg; got != "T0200" {
		t.Errorf("jump by name went to %s, want T0200", got)
	}
}

func TestStocksSortByAnotherColumnKeepsTheCursorOnItsStock(t *testing.T) {
	h := newHarness(t)
	h.openStocks()
	h.mustContain("Stocks", "NEWS▼") // mentions, largest first: NVDA, KO
	if row, _ := h.m.row(); row.Go.Arg != "NVDA" {
		t.Fatalf("cursor on %v, want NVDA first", row.Go)
	}
	h.press("<") // the column before mentions is the name
	h.mustContain("NAME▲", "sorted by name ascending")
	if row, _ := h.m.row(); row.Go == nil || row.Go.Arg != "NVDA" {
		t.Errorf("after sorting the cursor must stay on NVDA, it is on %+v", row)
	}
	if at, _ := h.m.vp.Row(); h.m.scr.Rows[at].Go.Arg != "NVDA" || h.m.vp.Cursor() != 1 {
		t.Errorf("NVDA should now be second (Coca-Cola, Nvidia): cursor index %d", h.m.vp.Cursor())
	}
	h.press("I")
	h.mustContain("NAME▼", "name descending")
	if h.m.vp.Cursor() != 0 {
		t.Errorf("inverted, NVDA is first again: %d", h.m.vp.Cursor())
	}
	// The choice belongs to the kind of screen: leaving and coming back keeps it.
	h.press("H")
	h.openStocks()
	h.mustContain("NAME▼")
	h.press("S")
	h.mustContain("HEALTH▲")
}

func TestScreensWithoutColumnsSayTheyCannotBeSorted(t *testing.T) {
	h := newHarness(t)
	h.press(">")
	if !h.m.failed || !strings.Contains(h.m.status, "no columns") {
		t.Errorf("status %q", h.m.status)
	}
}

func TestSourcesAreSelectableSortableAndFollowedAcrossARefresh(t *testing.T) {
	h := newHarness(t)
	h.press("S")
	h.press("down") // bad, good: the cursor goes to the second
	if row, _ := h.m.row(); row.Key != "source:good" {
		t.Fatalf("cursor on %+v, want source good", row)
	}
	// The good source fails; it moves above the other, and the cursor goes along.
	h.be.sources = []status.Source{
		{ID: "good", Kind: "rss", Enabled: true, Level: status.Red, Failures: 9, LastError: "gone"},
		{ID: "bad", Kind: "rss", Enabled: true, Level: status.Yellow},
	}
	h.press("r")
	if row, _ := h.m.row(); row.Key != "source:good" {
		t.Errorf("after the refresh the cursor is on %+v, want it to follow good", row)
	}
	if h.m.vp.Cursor() != 0 {
		t.Errorf("good is now the first row, the cursor is on %d", h.m.vp.Cursor())
	}
}

func TestCompaniesSortWithTheColumnKeysAndTheQueryWinsWhenTyped(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.mustContain("TICKER▲")
	h.press(">") // name
	h.mustContain("NAME▲")
	if got := h.m.scr.Sort; got.Key != "name" {
		t.Fatalf("sort = %+v", got)
	}
	first := func() string { row, _ := h.m.row(); return row.Go.Arg }
	h.press("home", "down") // the heading of the first group, then its first stock
	if first() != "GOOG" {
		t.Errorf("by name Alphabet comes first: %s", first())
	}
	h.press("I")
	h.press("home", "down")
	if first() != "NVDA" {
		t.Errorf("by name reversed, Nvidia comes first: %s", first())
	}
	// Typing a query with its own sort: takes over from the column keys.
	h.press("/")
	h.typeText("sort:-ticker")
	h.press("enter")
	h.mustContain("TICKER▼")
	h.press("home", "down")
	if first() != "NVDA" {
		t.Errorf("sort:-ticker lists NVDA first: %s", first())
	}
}

func TestMarkStocksThenTagThemAllAtOnce(t *testing.T) {
	h := newHarness(t)
	h.press("c")                       // grouped by exchange: Nasdaq (GOOG, GOOGL, NVDA), then NYSE (KO)
	h.press("space", "space", "space") // GOOG, GOOGL, NVDA; the cursor ends on the NYSE heading
	if len(h.m.marks) != 3 || !h.m.marks["NVDA"] {
		t.Fatalf("marks = %v", h.m.marks)
	}
	h.mustContain("●", "3 marked")
	h.press("down", "space") // past the NYSE heading to KO, the last row: marked, the cursor stays
	h.press("up", "up")      // the heading, then NVDA
	h.press("space")         // NVDA unmarked again
	if got := slices.Sorted(maps.Keys(h.m.marks)); !slices.Equal(got, []string{"GOOG", "GOOGL", "KO"}) {
		t.Fatalf("marks = %v", got)
	}
	h.press("#")
	h.mustContain("3 marked stocks")
	h.typeText("watchlist")
	h.press("enter")
	h.mustContain("3 marked stocks tags +watchlist")
	for tk, want := range map[string]bool{"GOOG": true, "GOOGL": true, "KO": true, "NVDA": false} {
		p, _ := h.be.Universe().Get(tk)
		if slices.Contains(p.Tags, "watchlist") != want {
			t.Errorf("%s tags %v, tagged should be %v", tk, p.Tags, want)
		}
	}
	h.press("U")
	if len(h.m.marks) != 0 {
		t.Errorf("U clears the marks: %v", h.m.marks)
	}
}

func TestMarkNeedsAStock(t *testing.T) {
	h := newHarness(t)
	h.press("S")
	h.press("space")
	if !h.m.failed || len(h.m.marks) != 0 {
		t.Errorf("a source cannot be marked: %q %v", h.m.status, h.m.marks)
	}
}

func TestFilterBarRecallsThePreviousSearchAndEditsInPlace(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText("nvidia")
	h.press("enter")
	h.mustContain("Search: nvidia")
	h.press("esc")
	h.press("/", "up")
	if got := h.m.prompt.Text(); got != "nvidia" {
		t.Fatalf("up in the filter bar recalls %q, want the earlier search", got)
	}
	h.press("left", "left", "left", "left", "left", "left")
	h.typeText("-")
	if got := h.m.prompt.Text(); got != "-nvidia" {
		t.Errorf("typing at the cursor: %q", got)
	}
	h.mustContain("/ -█nvidia")
}

// barLine is the key bar as plain text.
func (h *harness) barLine() string {
	lines := strings.Split(ansi.Strip(h.view()), "\n")
	return lines[len(lines)-2]
}

func TestKeyBarOffersOnlyTheKeysThatWorkHere(t *testing.T) {
	h := newHarness(t)
	h.m.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	for _, w := range []string{"filter", "help", "quit"} {
		if !strings.Contains(h.barLine(), w) {
			t.Errorf("home bar %q lacks %q", h.barLine(), w)
		}
	}
	for _, w := range []string{"jump", "sort", "mark", "tag"} {
		if strings.Contains(h.barLine(), w) {
			t.Errorf("home bar %q offers %q, which does nothing there", h.barLine(), w)
		}
	}
	h.press("c")
	for _, w := range []string{"jump", "sort", "invert", "mark", "tag", "list"} {
		if !strings.Contains(h.barLine(), w) {
			t.Errorf("catalogue bar %q lacks %q", h.barLine(), w)
		}
	}
	h.press("S")
	if bar := h.barLine(); !strings.Contains(bar, "sort") || strings.Contains(bar, "tag") || strings.Contains(bar, "open") {
		t.Errorf("sources bar %q: sortable, with nothing to tag or open", bar)
	}
}

func TestKeyBarInAnOpenArticle(t *testing.T) {
	h := newHarness(t)
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.press("down", "down", "down")
	for i := 0; i < 12 && h.m.selected() == nil; i++ {
		h.press("down")
	}
	h.press("enter")
	if h.m.detail == nil {
		t.Fatal("no article opened")
	}
	bar := h.barLine()
	for _, w := range []string{"keep", "drop", "browser", "back"} {
		if !strings.Contains(bar, w) {
			t.Errorf("article bar %q lacks %q", bar, w)
		}
	}
	if strings.Contains(bar, "filter") || strings.Contains(bar, "sort") {
		t.Errorf("article bar %q offers keys that are quiet in an article", bar)
	}
	h.press("s")
	if h.m.detail == nil || h.m.stack.Current().Kind != nav.Home {
		t.Error("a screen key must not leave an open article")
	}
}

func TestKeyBarKeepsHelpAndQuitWhenNarrow(t *testing.T) {
	h := newHarness(t)
	h.m.Update(tea.WindowSizeMsg{Width: 44, Height: 20})
	bar := h.barLine()
	if !strings.Contains(bar, "help") || !strings.Contains(bar, "quit") {
		t.Errorf("narrow bar %q must keep help and quit", bar)
	}
	if n := len([]rune(bar)); n > 44 {
		t.Errorf("bar is %d cells wide in 44", n)
	}
}

func TestAKeyThatDoesNothingHereSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.press("g")
	if !h.m.failed || !strings.Contains(h.m.status, "companies screen only") {
		t.Errorf("g on home: %q", h.m.status)
	}
	h.press("S", "#")
	if !h.m.failed || !strings.Contains(h.m.status, "stock") {
		t.Errorf("# on sources: %q", h.m.status)
	}
	h.press("o")
	if !h.m.failed || !strings.Contains(h.m.status, "web address") {
		t.Errorf("o on a source: %q", h.m.status)
	}
}

func TestMetersSummariseSourcesNewsAndUnread(t *testing.T) {
	h := newHarness(t)
	line := ansi.Strip(strings.Split(h.view(), "\n")[1])
	for _, want := range []string{"sources 1 ok 1 failing", "news 4 articles, 3 relevant", "2 unread"} {
		if !strings.Contains(line, want) {
			t.Errorf("meter line %q lacks %q", line, want)
		}
	}
	h.be.sources = append(h.be.sources, status.Source{
		ID: "later", Enabled: true, Level: status.Green, State: status.Idle, Until: now.Add(90 * time.Second),
	})
	h.press("r")
	line = ansi.Strip(strings.Split(h.view(), "\n")[1])
	if !strings.Contains(line, "sources 2 ok 1 failing") || !strings.Contains(line, "next fetch later in 1:30") {
		t.Errorf("after a refresh the meters follow the sources: %q", line)
	}
	h.be.sources[2].Until = now.Add(-5 * time.Minute)
	h.press("r")
	if line = ansi.Strip(strings.Split(h.view(), "\n")[1]); !strings.Contains(line, "later overdue 5:00") {
		t.Errorf("a source past its time says so: %q", line)
	}
}

func TestRefreshHighlightsWhatCameInOrChanged(t *testing.T) {
	h := newHarness(t)
	h.press("S")
	if len(h.m.fresh) != 0 {
		t.Fatalf("a screen just opened has nothing new: %v", h.m.fresh)
	}
	h.be.sources = append(h.be.sources, status.Source{ID: "newcomer", Kind: "rss", Enabled: true, Level: status.Green})
	h.be.sources[0].Failures = 4 // good now has a failure count: its row changes
	h.press("r")
	if !h.m.fresh["k:source:newcomer"].After(now) || !h.m.fresh["k:source:good"].After(now) {
		t.Fatalf("fresh = %v, want newcomer and good", h.m.fresh)
	}
	if _, ok := h.m.fresh["k:source:bad"]; ok {
		t.Error("bad did not change")
	}
	h.mustContain("2 new or changed")
	if !strings.Contains(h.view(), "\x1b[1;92m") && !strings.Contains(h.view(), "\x1b[1;38;5;10m") {
		t.Errorf("fresh rows are drawn in bright bold green:\n%q", h.view())
	}
	// Sorting moves rows but changes no text: nothing becomes fresh.
	clear(h.m.fresh)
	h.press("I")
	if len(h.m.fresh) != 0 {
		t.Errorf("a re-sort marked %v", h.m.fresh)
	}
	// Moving to another screen forgets what was fresh.
	h.m.fresh["x"] = now.Add(time.Hour)
	h.press("D")
	if len(h.m.fresh) != 0 {
		t.Errorf("fresh carried to another screen: %v", h.m.fresh)
	}
}

func TestFreshRowsFadeWithTime(t *testing.T) {
	h := newHarness(t)
	h.press("S")
	h.be.sources = append(h.be.sources, status.Source{ID: "newcomer", Kind: "rss", Enabled: true, Level: status.Green})
	h.press("r")
	row := Row{Key: "source:newcomer"}
	if !h.m.isFresh(row) {
		t.Fatal("not fresh right after the refresh")
	}
	h.m.fresh["k:source:newcomer"] = now.Add(-time.Second)
	if h.m.isFresh(row) {
		t.Error("still fresh after its time")
	}
}

func TestSKeyListsEveryStockWithTheOnesInTheNewsFirst(t *testing.T) {
	h := newHarness(t)
	h.press("s")
	h.mustContain("Companies: sort:news group:none", "4 of 4 companies")
	if row, _ := h.m.row(); row.Go == nil || row.Go.Arg != "NVDA" {
		t.Errorf("the stock with most news comes first, got %+v", row)
	}
	if h.m.vp.Len() != 4 {
		t.Errorf("all %d stocks are rows, not only those in the news", h.m.vp.Len())
	}
}

func TestCatalogueIsGroupedByExchangeAndXChangesTheGrouping(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.mustContain("Exchange Nasdaq · 3", "Exchange NYSE · 1")
	v := ansi.Strip(h.view())
	if strings.Index(v, "Exchange Nasdaq") > strings.Index(v, "GOOG ") || strings.Index(v, "Exchange NYSE") < strings.Index(v, "NVDA") {
		t.Errorf("each stock sits under its exchange, the bigger first:\n%s", v)
	}
	if h.m.vp.Len() != 6 || h.m.companies != 4 {
		t.Errorf("4 stocks and 2 headings to stop on: %d rows, %d companies", h.m.vp.Len(), h.m.companies)
	}
	h.press("x")
	h.mustContain("grouped by kind", "No kind · 4")
	h.press("x", "x")
	if h.m.scr.Group != "none" || strings.Contains(ansi.Strip(h.view()), " · 4") {
		t.Errorf("none: a flat list, group %q", h.m.scr.Group)
	}
	h.press("x")
	h.mustContain("Exchange Nasdaq")
}

func TestGroupOperatorAndSortWorkInsideGroups(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none sort:-ticker"})
	h.run(h.m.loadCmd(false))
	if h.m.scr.Group != "none" {
		t.Fatalf("group:none in the query: %q", h.m.scr.Group)
	}
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "sort:-ticker"})
	h.run(h.m.loadCmd(false))
	v := ansi.Strip(h.view())
	if strings.Index(v, "GOOGL") > strings.Index(v, "GOOG ") || !strings.Contains(v, "Exchange NYSE") {
		t.Errorf("reversed tickers inside each exchange group:\n%s", v)
	}
}

func TestAGroupHeadingOpensItsGroup(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	h.press("up") // from the first stock to the heading of Nasdaq
	h.mustContain("enter opens the group")
	h.press("down", "down", "down", "down") // GOOG GOOGL NVDA, then the NYSE heading
	h.press("enter")
	h.mustContain(`exchange:"NYSE"`, "1 of 4 companies", "KO")
	if h.m.scr.Group != "none" || h.m.vp.Len() != 1 {
		t.Errorf("a group opens flat with its stocks: group %q, %d rows", h.m.scr.Group, h.m.vp.Len())
	}
	if strings.Contains(ansi.Strip(h.view()), "NVDA") {
		t.Error("the other exchange is gone")
	}
}
