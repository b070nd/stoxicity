package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/pages"
	"github.com/charmbracelet/x/ansi"
)

func calendarFixture() marketdata.PriceCalendar {
	var rows []marketdata.Candle
	for _, r := range []struct {
		day   string
		price float64
	}{{"2025-11-28", 100}, {"2025-12-31", 110}, {"2026-01-30", 110}, {"2026-02-27", 99}} {
		rows = append(rows, marketdata.Candle{Ticker: "NVDA", Day: r.day, Open: r.price, High: r.price, Low: r.price, Close: r.price, Raw: true, Currency: "USD"})
	}
	return marketdata.BuildPriceCalendar(now, rows, nil, marketdata.SplitCoverage{})
}

func TestPriceCalendarBarsHaveMonthlyColorsAndPreserveGaps(t *testing.T) {
	calendar := calendarFixture()
	c := priceCalendarCell(calendar)
	if c.Text != "██▁·········" || len(c.Spans) != 12 {
		t.Fatalf("calendar strip: %+v", c)
	}
	for i, style := range []Style{Good, Warn, Bad, Dim} {
		if c.Spans[i].Style != style {
			t.Errorf("month %d style: %v want %v", i, c.Spans[i].Style, style)
		}
	}
	calendar.Months[0].Unresolved, calendar.Months[0].HasChange = true, false
	if c := priceCalendarCell(calendar); []rune(c.Text)[0] != '?' || c.Spans[0].Style != Dim {
		t.Fatalf("unresolved colored as performance: %+v", c)
	}
	// Padding and the selected-row renderer preserve each Unicode bar's style.
	table := Table{Columns: []Column{{Title: "NAME", Width: 8}, {Title: "12M", Width: 12}}}
	text, spans := table.line([]Cell{Str("株"), c})
	for i, span := range spans {
		if string([]rune(text)[span.From:span.To]) != string([]rune(c.Text)[i]) {
			t.Fatalf("bad Unicode offset: %+v in %q", span, text)
		}
	}
	if got := ansi.Strip(spanned(styleDim.Reverse(true), "  ", text, spans)); got != "  "+text {
		t.Fatalf("selected calendar lost text: %q", got)
	}
}

func TestPriceCalendarUsesAllEightPriceLevelsAndFlatSeries(t *testing.T) {
	var calendar marketdata.PriceCalendar
	for i := 0; i < 8; i++ {
		calendar.Months[i] = marketdata.PriceMonth{PricePeriod: marketdata.PricePeriod{Day: "stored", Close: float64(i + 1), HasChange: true}}
	}
	if got := priceCalendarCell(calendar).Text; got != "▁▂▃▄▅▆▇█····" {
		t.Fatalf("price levels: %q", got)
	}
	for i := 0; i < 8; i++ {
		calendar.Months[i].Close = 100
	}
	flat := priceCalendarCell(calendar)
	if flat.Text != "▄▄▄▄▄▄▄▄····" || flat.Spans[0].Style != Warn {
		t.Fatalf("flat series: %+v", flat)
	}
}

func TestPriceYearsStripHasTenIndividuallyColoredBars(t *testing.T) {
	calendar := marketdata.PriceCalendar{Years: make([]marketdata.PriceYear, 10)}
	for i, point := range []marketdata.PricePeriod{
		{Day: "2017-12-29", Close: 110, HasChange: true, Change: .1},
		{Day: "2018-12-31", Close: 110, HasChange: true},
		{Day: "2019-12-31", Close: 99, HasChange: true, Change: -.1},
	} {
		calendar.Years[i] = marketdata.PriceYear{PricePeriod: point}
	}
	cell := priceYearsCell(calendar)
	if cell.Text != "██▁·······" || len(cell.Spans) != 10 {
		t.Fatalf("annual strip: %+v", cell)
	}
	for i, style := range []Style{Good, Warn, Bad, Dim} {
		if cell.Spans[i].Style != style {
			t.Fatalf("year %d: style %v want %v", i, cell.Spans[i].Style, style)
		}
	}
}

func TestCalendarAppearsInCatalogueAndStockProfile(t *testing.T) {
	h := newHarness(t)
	h.be.mu.Lock()
	h.be.priceCalendars = map[string]marketdata.PriceCalendar{"NVDA": calendarFixture()}
	h.be.quotes = map[string]Quote{"NVDA": {Day: "2026-11-04", Open: 100, High: 112, Low: 99, Close: 110, Prev: 100, PrevDay: "2026-11-03"}}
	h.be.mu.Unlock()
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none"})
	h.run(h.m.loadCmd(false))
	var nvda, missing Row
	for _, row := range h.m.scr.Rows {
		if row.Go != nil && row.Go.Kind == nav.Stock {
			if row.Go.Arg == "NVDA" {
				nvda = row
			}
			if row.Go.Arg == "KO" {
				missing = row
			}
		}
	}
	if !strings.Contains(nvda.Text, "██▁·········") || !strings.Contains(missing.Text, "············") {
		t.Fatalf("missing per-stock strip: %q / %q", nvda.Text, missing.Text)
	}
	for _, row := range h.m.scr.Rows {
		if row.TableHeader && (!strings.Contains(row.Text, "DAY") || !strings.Contains(row.Text, "12M") || !strings.Contains(row.Text, "10Y")) {
			t.Fatalf("overview should retain candle, monthly and annual strips: %q", row.Text)
		}
		if row.TableHeader {
			monthly, annual := strings.Index(row.Text, "12M"), strings.Index(row.Text, "10Y")
			if annual <= monthly || strings.TrimSpace(row.Text[monthly+3:annual]) != "" {
				t.Fatalf("annual strip must sit beside monthly strip: %q", row.Text)
			}
		}
	}
	h.run(h.m.goTo(nav.Route{Kind: nav.Stocks}))
	found := false
	for _, row := range h.m.scr.Rows {
		if row.Go != nil && row.Go.Arg == "NVDA" && strings.Contains(row.Text, "██▁·········") {
			found = true
		}
	}
	if !found {
		t.Fatal("related-stock overview is missing the compact strip")
	}
	for _, width := range []int{80, 40} {
		d := h.m.data
		d.Width = width
		s := Build(nav.Route{Kind: nav.Stock, Arg: "NVDA"}, d)
		var text strings.Builder
		for _, row := range s.Rows {
			text.WriteString(row.Text + "\n")
			if strings.Contains(row.Text, "Dec25") && cellWidth(row.Text) > width {
				t.Fatalf("calendar does not fit %d: %q", width, row.Text)
			}
			for _, span := range row.Spans {
				if span.From < 0 || span.To > utf8.RuneCountInString(row.Text) {
					t.Fatalf("span beyond row: %+v", span)
				}
			}
		}
		for _, want := range []string{"12-month price calendar", "Dec25", "Nov26*", "+10.0%", "+0.0%", "-10.0%", "110.00", "excludes dividends"} {
			if !strings.Contains(text.String(), want) {
				t.Errorf("missing %q at width %d", want, width)
			}
		}
		catalogue := Build(nav.Route{Kind: nav.Companies, Arg: "group:none"}, d)
		for _, row := range catalogue.Rows {
			if width == 80 && row.TableHeader && (!strings.Contains(row.Text, "DAY") || !strings.Contains(row.Text, "12M") || !strings.Contains(row.Text, "10Y")) {
				t.Fatalf("80-column overview lost a chart: %q", row.Text)
			}
			if row.Go != nil && row.Go.Kind == nav.Stock && cellWidth(row.Text) > width {
				t.Fatalf("catalogue calendar does not fit %d: %q", width, row.Text)
			}
		}
	}
}

func TestCalendarReadFailureIsNotReportedAsFlatOrMissingHistory(t *testing.T) {
	f := newFake()
	d := Data{Universe: f.Universe(), Now: now, PriceCalendarError: "fixture database unavailable"}
	s := Build(nav.Route{Kind: nav.Stock, Arg: "NVDA"}, d)
	var text strings.Builder
	for _, row := range s.Rows {
		text.WriteString(row.Text)
	}
	if !strings.Contains(text.String(), "Price history unavailable: fixture database unavailable") || strings.Contains(text.String(), "No stored price history") {
		t.Fatalf("read failure hidden: %s", text.String())
	}
}

func TestPriceCalendarLabelsUseConfiguredTimeZone(t *testing.T) {
	d := Data{Now: time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC), Params: pages.Params{Zone: time.FixedZone("local", 3600)}, PriceCalendars: map[string]marketdata.PriceCalendar{}}
	calendar := d.priceCalendar("NVDA")
	if calendar.Months[0].Month != "2025-12" || calendar.Months[11].Month != "2026-11" {
		t.Fatalf("calendar used UTC month: %+v", calendar)
	}
	var b builder
	priceCalendarRows(&b, "NVDA", d)
	var text strings.Builder
	for _, row := range b.rows {
		text.WriteString(row.Text)
	}
	if !strings.Contains(text.String(), "Nov26*") || strings.Contains(text.String(), "Oct26*") {
		t.Fatalf("wrong current-month marker: %s", text.String())
	}
}

func TestCatalogueWithPriceCalendarsFiltersQuickly(t *testing.T) {
	h := newHarness(t)
	h.bigCatalogue(12000)
	h.be.mu.Lock()
	h.be.priceCalendars = map[string]marketdata.PriceCalendar{}
	calendar := calendarFixture()
	for _, profile := range h.be.univ.All() {
		h.be.priceCalendars[profile.Ticker] = calendar
	}
	h.be.mu.Unlock()
	h.press("c")
	start := time.Now()
	for _, key := range []string{"/", "c", "o", "m", "p"} {
		h.press(key)
	}
	per := time.Since(start) / 5
	if h.m.vp.Len() == 0 {
		t.Fatal("no catalogue rows")
	}
	// Both colored strips render in about 40ms without instrumentation, about
	// 410ms with -race. Allow detector overhead while guarding catalogue stalls.
	if per > 500*time.Millisecond {
		t.Errorf("calendar filtering takes %v per key", per)
	}
	t.Logf("%v per key with 12,000 price calendars", per)
}
