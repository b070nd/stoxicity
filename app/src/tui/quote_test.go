package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/b070nd/stoxicity/app/src/nav"
)

var quoteNow = time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)

func TestCandleBarDrawsTheDayFromLowToHigh(t *testing.T) {
	for _, c := range []struct {
		name  string
		q     Quote
		bar   string
		style Style
	}{
		{"closed near the high", Quote{Day: "2026-11-05", Open: 10, High: 20, Low: 10, Close: 20}, "████████", Good},
		{"opened high, closed low", Quote{Day: "2026-11-05", Open: 19, High: 20, Low: 10, Close: 11}, "─██████─", Bad},
		{"a doji in the middle", Quote{Day: "2026-11-05", Open: 15, High: 20, Low: 10, Close: 15}, "────┿───", Dim},
		{"no range at all", Quote{Day: "2026-11-05", Open: 5, High: 5, Low: 5, Close: 5}, "────┿───", Dim},
		{"yesterday's candle on a quiet week is dim", Quote{Day: "2026-10-20", Open: 10, High: 20, Low: 10, Close: 20}, "████████", Dim},
	} {
		bar, style := candleBar(c.q, candleWidth, quoteNow)
		if bar != c.bar || style != c.style {
			t.Errorf("%s: %q %v, want %q %v", c.name, bar, style, c.bar, c.style)
		}
	}
	if bar, _ := candleBar(Quote{Day: "2026-11-05", High: 1, Low: 2}, candleWidth, quoteNow); bar != "" {
		t.Errorf("a candle with its low over its high drew %q", bar)
	}
}

func TestChangeAndPriceText(t *testing.T) {
	up := Quote{Day: "2026-11-05", Close: 102, Prev: 100}
	if s, st := changeText(up, quoteNow); s != "+2.00%" || st != Good {
		t.Errorf("up: %q %v", s, st)
	}
	down := Quote{Day: "2026-11-05", Close: 98.5, Prev: 100}
	if s, st := changeText(down, quoteNow); s != "-1.50%" || st != Bad {
		t.Errorf("down: %q %v", s, st)
	}
	if s, st := changeText(Quote{Day: "2026-11-05", Close: 100, Prev: 100}, quoteNow); s != "+0.00%" || st != Dim {
		t.Errorf("flat: %q %v", s, st)
	}
	if s, _ := changeText(Quote{Day: "2026-11-05", Close: 100}, quoteNow); s != "" {
		t.Errorf("no day before: %q", s)
	}
	if priceText(71.9) != "71.90" || priceText(54321.4) != "54,321" {
		t.Errorf("priceText: %q %q", priceText(71.9), priceText(54321.4))
	}
}

func TestTableCellsColourOnlyTheirOwnColumn(t *testing.T) {
	tb := Table{Columns: []Column{{Title: "A", Width: 4}, {Title: "N", Width: 6, Right: true}, {Title: "Z", Width: 3}, {Title: "REST"}}}
	text, spans := tb.line([]Cell{Str("ab"), Num("1.5", 1.5).Styled(Good), Str("").Styled(Bad), Str("tail").Styled(Info)})
	if text != "ab      1.5     tail" {
		t.Fatalf("text %q", text)
	}
	got := map[Style]string{}
	for _, sp := range spans {
		got[sp.Style] = string([]rune(text)[sp.From:sp.To])
	}
	if got[Good] != "1.5" || got[Info] != "tail" || len(spans) != 2 {
		t.Errorf("spans %+v over %v", spans, got)
	}
}

func TestSpannedKeepsTheTextWhateverTheStyles(t *testing.T) {
	out := spanned(styleDim, " ●", "AAPL  ▲ 3.2%", []Span{{From: 6, To: 12, Style: Good}, {From: 0, To: 4, Style: Info}})
	if ansi.Strip(out) != " ●AAPL  ▲ 3.2%" {
		t.Errorf("stripped %q", ansi.Strip(out))
	}
}

func (h *harness) quotesFor(q map[string]Quote) {
	h.be.mu.Lock()
	h.be.quotes = q
	h.be.mu.Unlock()
}

func TestCompaniesShowTypeDomainCandleCloseAndChange(t *testing.T) {
	h := newHarness(t)
	h.quotesFor(map[string]Quote{
		"NVDA": {Day: "2026-11-04", Open: 100, High: 112, Low: 99, Close: 110, Prev: 100},
		"KO":   {Day: "2026-11-04", Open: 60, High: 61, Low: 58, Close: 59, Prev: 60},
	})
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none"})
	h.run(h.m.loadCmd(false))
	h.mustContain("TYPE", "DOMAIN", "DAY", "CLOSE", "CHG", "110.00", "+10.00%", "59.00", "-1.67%", "Manufacturing")

	var nvda Row
	for _, r := range h.m.scr.Rows {
		if r.Go != nil && r.Go.Arg == "NVDA" {
			nvda = r
		}
	}
	text := []rune(nvda.Text)
	painted := map[Style][]string{}
	for _, sp := range nvda.Spans {
		painted[sp.Style] = append(painted[sp.Style], string(text[sp.From:sp.To]))
	}
	if !slices.Contains(painted[Good], "110.00") || !slices.Contains(painted[Good], "+10.00%") {
		t.Errorf("a day that closed up is green: %v in %q", painted, nvda.Text)
	}
	if want := domainStyle("Manufacturing"); !slices.Contains(painted[want], "Manufacturing") {
		t.Errorf("the domain is coloured by its division: %v", painted)
	}
	// The candle is a real bar of the day's range.
	if !strings.ContainsAny(nvda.Text, "█┿") || !strings.Contains(nvda.Text, "─") {
		t.Errorf("no candle in %q", nvda.Text)
	}
	// The row is no wider than the table's columns, so the line fits the screen.
	for _, r := range h.m.scr.Rows {
		if len(r.Spans) > 0 {
			end := r.Spans[len(r.Spans)-1].To
			if end > len([]rune(r.Text)) {
				t.Fatalf("a span past the end of %q: %+v", r.Text, r.Spans)
			}
		}
	}
}

func TestCompaniesSortByChangeAndWithoutPricesNoPriceColumns(t *testing.T) {
	h := newHarness(t)
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none"})
	h.run(h.m.loadCmd(false))
	h.mustContain("TYPE", "DOMAIN", "no prices yet")
	if v := h.view(); strings.Contains(v, "CLOSE") || strings.Contains(v, "CHG") {
		t.Errorf("price columns without prices:\n%s", v)
	}

	h.quotesFor(map[string]Quote{
		"NVDA": {Day: "2026-11-04", Open: 100, High: 112, Low: 99, Close: 110, Prev: 100},
		"KO":   {Day: "2026-11-04", Open: 60, High: 61, Low: 58, Close: 59, Prev: 60},
	})
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "sort:-change group:none"})
	h.run(h.m.loadCmd(false))
	var order []string
	for _, r := range h.m.scr.Rows {
		if r.Go != nil && r.Go.Kind == nav.Stock {
			order = append(order, r.Go.Arg)
		}
	}
	if len(order) < 2 || order[0] != "NVDA" || order[1] != "KO" {
		t.Errorf("by change, biggest rise first: %v", order)
	}
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "type:etf"})
	h.run(h.m.loadCmd(false))
	if h.m.scr.Notice == "" {
		t.Errorf("type:etf matches none of the fixture's stocks: %v", h.m.scr.Rows)
	}
}

func TestStockPageShowsTheLastCloseAndTheDay(t *testing.T) {
	h := newHarness(t)
	h.quotesFor(map[string]Quote{"NVDA": {Day: "2026-11-04", Open: 100, High: 112, Low: 99, Close: 110, Prev: 100}})
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "NVDA"})
	h.run(h.m.loadCmd(false))
	h.mustContain("last close", "110.00 on 2026-11-04 (+10.00%)", "low 99.00 · open 100.00 · close 110.00 · high 112.00")
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "KO"})
	h.run(h.m.loadCmd(false))
	if strings.Contains(h.view(), "last close") {
		t.Error("KO has no candle but the page shows a last close")
	}
}

func TestCompanyRowsAreNeverCutAndEachKindOfFactHasItsColour(t *testing.T) {
	h := newHarness(t)
	h.be.mu.Lock()
	h.be.layer.AddTag("NVDA", "moat")
	h.be.rebuildUniverse()
	h.be.mu.Unlock()
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none"})
	h.run(h.m.loadCmd(false))
	var nvda, ko Row
	for _, r := range h.m.scr.Rows {
		if r.Go != nil && r.Go.Arg == "NVDA" {
			nvda = r
		}
		if r.Go != nil && r.Go.Arg == "KO" {
			ko = r
		}
		if strings.Contains(r.Text, "…") {
			t.Errorf("a row was cut: %q", r.Text)
		}
	}
	paint := func(r Row) map[Style]string {
		out := map[Style]string{}
		rs := []rune(r.Text)
		for _, sp := range r.Spans {
			out[sp.Style] += string(rs[sp.From:sp.To]) + "|"
		}
		return out
	}
	p := paint(nvda)
	if p[Strong] != "NVDA|" || p[exchangeStyle("Nasdaq")] != "Nasdaq|" || p[Warn] != "2|" || p[Accent] != "#moat|" {
		t.Errorf("NVDA is painted %v in %q", p, nvda.Text)
	}
	if k := paint(ko); k[Warn] != "★|1|" || k[exchangeStyle("NYSE")] != "NYSE|" {
		t.Errorf("KO is painted %v in %q", k, ko.Text)
	}
	// The full name closes the line, after everything else.
	if !strings.HasSuffix(nvda.Text, "Nvidia Corp") {
		t.Errorf("the name is not the last column: %q", nvda.Text)
	}
}

func TestToneAndNewsStylesCoverTheirRange(t *testing.T) {
	for v, want := range map[float64]Style{0.5: Good, 0.05: Good, 0.01: Dim, -0.01: Dim, -0.05: Bad, -0.9: Bad} {
		if got := toneStyle(v); got != want {
			t.Errorf("toneStyle(%v) = %v, want %v", v, got, want)
		}
	}
	if newsStyle(0) != Plain || newsStyle(1) != Warn || newsStyle(4) != Warn || newsStyle(5) != Hot {
		t.Error("newsStyle tiers")
	}
}
