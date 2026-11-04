package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
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
	sources  []SourceRow
	verdicts map[string]labels.Verdict

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
	return &fake{
		arts: []model.Article{
			art("a1", "Nvidia beats estimates", "sec.gov", 2*time.Hour, 0.9, 4, []string{"NVDA"}, []string{"ai-infra"}),
			art("a2", "Coca-Cola raises dividend", "example.com", 30*time.Hour, 0.8, 3, []string{"KO"}, []string{"dividends"}),
			art("a3", "Meme stock chatter", "noise.net", 5*time.Hour, 0.1, 1, nil, nil),
			art("a4", "Nvidia supply chain", "example.com", 50*time.Hour, 0.7, 3, []string{"NVDA"}, []string{"ai-infra"}),
		},
		watches: []WatchInfo{{ID: "tech", Name: "Tech", Unread: 2}, {ID: "buffett", Name: "Buffett core", Unread: 0}},
		sources: []SourceRow{
			{ID: "good", Kind: "rss", Enabled: true, Health: "green"},
			{ID: "bad", Kind: "rss", Enabled: true, Health: "red", LastError: "boom"},
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

func (f *fake) Sources(context.Context) ([]SourceRow, error) { return f.sources, nil }

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
		Backend: h.be, Refresh: time.Millisecond,
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
	case tickMsg:
		// The timer is not run in tests.
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
	h.press("s")
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
	h.press("s")
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
	if h.m.cursor != 1 {
		t.Errorf("cursor after back = %d, want 1", h.m.cursor)
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
	h.m.cursor = slices.Index(h.m.sel, idx)
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
	if string(h.m.filter) != "ab" {
		t.Errorf("filter = %q", string(h.m.filter))
	}
	h.press("space")
	if string(h.m.filter) != "ab " {
		t.Errorf("filter = %q", string(h.m.filter))
	}
	// Keys that are commands elsewhere are text in the filter bar.
	h.typeText("qs")
	if string(h.m.filter) != "ab qs" || h.m.quitting {
		t.Errorf("filter = %q, quitting = %v", string(h.m.filter), h.m.quitting)
	}
}

// selectArticle moves the cursor to the first article row.
func (h *harness) selectArticle(title string) {
	h.t.Helper()
	for i, r := range h.m.scr.Rows {
		if r.Article != nil && r.Article.Title == title {
			h.m.cursor = slices.Index(h.m.sel, i)
			h.m.follow()
			return
		}
	}
	h.t.Fatalf("no article row %q", title)
}

func (h *harness) selectArticleID(id string) {
	h.t.Helper()
	for i, r := range h.m.scr.Rows {
		if r.Article != nil && r.Article.ID == id {
			h.m.cursor = slices.Index(h.m.sel, i)
			h.m.follow()
			return
		}
	}
	h.t.Fatalf("no article row %q", id)
}

func TestArticleDetailShowsWhyAndLabels(t *testing.T) {
	h := newHarness(t)
	h.press("s", "enter") // NVDA
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
	h.be.arts[0].Summary = "before \x1b[31mred\x1b[0m after"
	h.press("r")
	for _, screen := range []string{h.view()} {
		if strings.Contains(screen, "\x1b]") || strings.Contains(screen, "owned") || strings.Contains(screen, "\x1b[2J") {
			t.Errorf("an escape sequence from a feed reached the screen:\n%q", screen)
		}
	}
	h.selectArticleID("a1")
	h.press("enter")
	if v := h.view(); strings.Contains(v, "\x1b[31m") || strings.Contains(v, "\x1b]") {
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
	m := New(context.Background(), Options{Backend: be, Watch: "gone", Refresh: time.Millisecond})
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
		at := h.m.sel[h.m.cursor]
		if at < h.m.top || at >= h.m.top+h.m.bodyHeight() {
			t.Fatalf("step %d: cursor row %d outside [%d, %d)", i, at, h.m.top, h.m.top+h.m.bodyHeight())
		}
	}
	h.press("up")
	h.m.move(-100)
	if h.m.cursor != 0 || h.m.top != 0 {
		t.Errorf("cursor %d, top %d after going to the top", h.m.cursor, h.m.top)
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
	s := Build(nav.Route{Kind: nav.Sources}, Data{Params: pagesParams(), Sources: []SourceRow{
		{ID: "off", Enabled: false, Health: "red"},
		{ID: "ok", Enabled: true, Health: "green"},
		{ID: "never", Enabled: true},
		{ID: "bad", Enabled: true, Health: "red"},
		{ID: "slow", Enabled: true, Health: "yellow"},
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
