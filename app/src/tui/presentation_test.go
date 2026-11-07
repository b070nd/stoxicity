package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/b070nd/stoxicity/app/src/nav"
)

func TestTabAndFocusedArrowsNavigateSectionsWithoutSwitchingWatches(t *testing.T) {
	h := newHarness(t)
	h.press("1")
	h.press("tab")
	if h.m.litTab() != "Stocks" || h.m.stack.Watch() != "tech" || !h.m.navFocus {
		t.Fatalf("tab: section=%s watch=%s focus=%t", h.m.litTab(), h.m.stack.Watch(), h.m.navFocus)
	}
	h.press("right")
	if h.m.litTab() != "Topics" {
		t.Fatal("right did not navigate the header")
	}
	h.press("shift+tab")
	if h.m.litTab() != "Stocks" {
		t.Fatal("shift+tab did not return to Stocks")
	}
	h.press("down")
	if h.m.navFocus {
		t.Fatal("down did not return to content")
	}
	h.press("enter")
	if h.m.stack.Current().Kind != nav.Stock {
		t.Fatal("content Enter did not open the stock")
	}
	h.press("f6", "left")
	if h.m.litTab() != "News" {
		t.Fatal("F6 did not give the header the arrows")
	}
}

func TestNewsRelevanceTogglePreservesEvidenceAndSelectedWatch(t *testing.T) {
	h := newHarness(t)
	if strings.Contains(h.rows(), "Meme stock chatter") {
		t.Fatal("irrelevant article in relevant-only feed")
	}
	h.press("v")
	if !strings.Contains(h.rows(), "Meme stock chatter") || h.m.relevantOnly {
		t.Fatal("all-news toggle did not reveal the article")
	}
	h.press("v")
	if strings.Contains(h.rows(), "Meme stock chatter") || len(h.m.shared) != 4 {
		t.Fatal("toggle mutated the source data")
	}
	h.press("1", "v")
	if h.m.stack.Watch() != "tech" {
		t.Fatal("relevance toggle cleared a custom watch")
	}
	bar := ansi.Strip(h.m.watchBar())
	if strings.Contains(bar, "All news") || strings.Contains(bar, "Tech") || strings.Count(bar, "News") != 1 {
		t.Fatalf("watch controls leaked into section navigation: %q", bar)
	}
}

func TestRelevantToggleClearsBundledUpstreamRelevantWatch(t *testing.T) {
	h := newHarness(t)
	h.be.watches = append(h.be.watches, WatchInfo{ID: "all-relevant", Name: "Everything relevant"})
	h.run(h.m.watchesCmd())
	h.run(h.m.switchWatch("all-relevant"))
	h.press("v")
	if h.m.stack.Watch() != "" || h.m.relevantOnly {
		t.Fatal("all-news view is still filtered by bundled watch")
	}
}

func TestRenderedTablePreservesPaddingAndSelectedCellColors(t *testing.T) {
	h := newHarness(t)
	h.quotesFor(map[string]Quote{
		"NVDA": {Day: "2026-11-04", Open: 112, High: 114, Low: 99, Close: 100, Prev: 110, PrevDay: "2026-11-03"},
	})
	h.press("s")
	row, _ := h.m.row()
	if row.Go == nil || row.Go.Arg != "NVDA" {
		t.Fatal("expected NVDA selected")
	}
	var rendered string
	for _, line := range h.m.bodyLines() {
		if strings.HasPrefix(ansi.Strip(line), "▸") {
			rendered = line
			break
		}
	}
	if ansi.Strip(rendered) != "▸ "+row.Text {
		t.Fatalf("table spacing lost:\n%q\n%q", ansi.Strip(rendered), row.Text)
	}
	if !strings.Contains(rendered, "31") {
		t.Fatalf("negative selected cells lost their red style: %q", rendered)
	}
	if !strings.Contains(rendered, "-9.09%") {
		t.Fatal("selected return is missing")
	}
	// A refreshed negative row remains negative; freshness only changes emphasis.
	h.m.fresh[row.Identity()] = now.Add(freshFor)
	for _, line := range h.m.bodyLines() {
		if strings.HasPrefix(ansi.Strip(line), "▸") && strings.Contains(line, "92m") {
			t.Fatal("freshness turned a negative row green")
		}
	}
}

func TestResponsiveTableKeepsPricesAndActiveSortVisible(t *testing.T) {
	h := newHarness(t)
	h.quotesFor(map[string]Quote{"NVDA": {Day: "2026-11-04", Open: 100, High: 112, Low: 99, Close: 110, Prev: 100, PrevDay: "2026-11-03"}})
	h.numbersFor(map[string]map[string]float64{
		"marketcap": {"NVDA": 2.7e12}, "revenue": {"NVDA": 6e10}, "growth": {"NVDA": 1.26}, "margin": {"NVDA": 0.49},
	})
	for _, width := range []int{40, 80, 120, 240} {
		h.m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		h.run(h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none sort:-marketcap"}))
		h.run(h.m.loadCmd(false))
		var header string
		for _, r := range h.m.scr.Rows {
			if r.TableHeader {
				header = r.Text
			}
			if (r.TableHeader || markable(r) != "") && ansi.StringWidth(r.Text) > width-2 {
				t.Errorf("width %d: table overflows: %q", width, r.Text)
			}
		}
		for _, key := range []string{"TICKER", "CLOSE", "CHG", "MCAP▼"} {
			if !strings.Contains(header, key) {
				t.Errorf("width %d: %s missing in %q", width, key, header)
			}
		}
	}
}

func TestSearchSuggestionsCompleteWithoutCommittingOrFetching(t *testing.T) {
	h := newHarness(t)
	h.press("s", "/")
	h.typeText("nvd")
	if len(h.m.suggestions) == 0 || h.m.suggestions[0].value != "NVDA" {
		t.Fatalf("no ticker suggestions: %+v", h.m.suggestions)
	}
	loads := len(h.be.articleCalls)
	h.press("down", "enter")
	if h.m.mode != modeFilter || !strings.HasSuffix(h.m.prompt.Text(), "NVDA") {
		t.Fatal("Enter did not complete selected ticker")
	}
	if h.m.stack.Current().Arg != stocksQuery || len(h.be.articleCalls) != loads {
		t.Fatal("completion committed or fetched data")
	}
	h.press("enter")
	if h.m.mode != modeList || !strings.Contains(h.m.stack.Current().Arg, "NVDA") {
		t.Fatal("second Enter did not apply query")
	}
	h.press("n", "/")
	h.typeText("nvd")
	h.press("tab")
	if h.m.prompt.Text() != "ticker:NVDA" || h.m.litTab() != "News" {
		t.Fatal("Tab in search navigated away instead of completing")
	}
	h.press("esc")
	if len(h.be.searchCalls) != 0 {
		t.Fatal("cancelled suggestions ran a news search")
	}
}

func TestStickyHeaderAndResizeKeepSelectedStockVisible(t *testing.T) {
	h := newHarness(t)
	h.bigCatalogue(100)
	h.press("s", "end")
	for _, width := range []int{80, 140} {
		h.m.Update(tea.WindowSizeMsg{Width: width, Height: 16})
		body := strings.Join(h.m.bodyLines(), "\n")
		if !strings.Contains(body, "TICKER") || !strings.Contains(body, "T0099") {
			t.Fatalf("header or selected stock lost:\n%s", ansi.Strip(body))
		}
	}
}

func TestTableUsesDisplayCellsForWideUnicode(t *testing.T) {
	tb := Table{Columns: []Column{{Title: "NAME", Width: 8}, {Title: "VALUE", Width: 6, Right: true}}}
	tb.Items = []Item{{Cells: []Cell{Str("東京"), Num("10", 10)}}, {Cells: []Cell{Str("Acme"), Num("20", 20)}}}
	rows := tb.Rows(Sort{})
	for _, r := range rows {
		if ansi.StringWidth(r.Text) != 15 {
			t.Errorf("misaligned display cells: %q", r.Text)
		}
	}
}

func TestEmptyRelevantFeedExplainsHowToShowAllNews(t *testing.T) {
	h := newHarness(t)
	h.be.arts = h.be.arts[2:3] // the low-relevance chatter article
	h.press("r")
	if !strings.Contains(h.m.scr.Notice, "Press v") {
		t.Fatalf("misleading empty-feed message: %s", h.m.scr.Notice)
	}
	h.press("v")
	if !strings.Contains(h.rows(), "Meme stock chatter") {
		t.Fatal("all-news view did not recover the article")
	}
}

func TestPresentationFramesFitTerminal(t *testing.T) {
	h := newHarness(t)
	h.quotesFor(map[string]Quote{
		"NVDA": {Day: "2026-11-04", Open: 100, High: 112, Low: 99, Close: 110, Prev: 100, PrevDay: "2026-11-03"},
		"KO":   {Day: "2026-11-04", Open: 60, High: 61, Low: 58, Close: 59, Prev: 60, PrevDay: "2026-11-03"},
	})
	for _, width := range []int{40, 80, 120, 200} {
		for _, height := range []int{8, 24, 40} {
			h.m.Update(tea.WindowSizeMsg{Width: width, Height: height})
			for _, key := range []string{"n", "s", "t", "e", "o", "w", "a"} {
				h.press(key)
				lines := strings.Split(h.view(), "\n")
				if len(lines) != height {
					t.Fatalf("%s: frame height %d, want %d", key, len(lines), height)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) != width {
						t.Fatalf("%s: frame width %d, want %d: %q", key, ansi.StringWidth(line), width, line)
					}
				}
			}
		}
	}
}

func TestSafeLinePreservesPaddingAndStripsUntrustedControls(t *testing.T) {
	if got := safeLine("  A  B  "); got != "  A  B  " {
		t.Fatalf("padding lost: %q", got)
	}
	if got := safeLine("  A\x1b[31m\u202e B\nC  "); got != "  A B C  " {
		t.Fatalf("unsafe controls or lost padding: %q", got)
	}
}
