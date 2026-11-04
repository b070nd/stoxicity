package pages

import (
	"reflect"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/tagcloud"
)

var berlin = mustZone("Europe/Berlin")

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// pageArticle is a synthetic scored article.
func pageArticle(id string, published time.Time, relevance float64, materiality int, kind model.EventKind, tickers, topics []string) model.Article {
	return model.Article{
		ID: id, SourceID: "s", Domain: "news.example.com", URL: "https://news.example.com/" + id,
		Title: "Title of article " + id + " about the business", Published: published,
		Tickers: tickers, Topics: topics,
		Score: model.Score{Relevance: relevance, Materiality: materiality, Kind: kind, LensVersion: "v1"},
	}
}

func utc(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, time.UTC) }

func TestDayRowsAcrossBerlinMidnight(t *testing.T) {
	// 2026-11-04 is the last day of the window. Berlin is UTC+1 in November.
	p := NewParams(time.Date(2026, 11, 4, 12, 0, 0, 0, berlin), 3, 10, 0.5, berlin)
	arts := []model.Article{
		pageArticle("late-nov2", utc(2, 22, 59), 0.9, 4, model.EventResults, nil, nil),     // 23:59 Nov 2 in Berlin
		pageArticle("early-nov3", utc(2, 23, 0), 0.6, 3, model.EventBuyback, nil, nil),     // 00:00 Nov 3
		pageArticle("late-nov3", utc(3, 22, 59), 0.2, 1, model.EventOther, nil, nil),       // 23:59 Nov 3
		pageArticle("early-nov4", utc(3, 23, 0), 0.7, 5, model.EventNonReliance, nil, nil), // 00:00 Nov 4
		pageArticle("too-old", utc(1, 22, 59), 0.9, 5, model.EventResults, nil, nil),       // 23:59 Nov 1, outside
	}
	rows := DayRows(InWindow(arts, p), p)
	type got struct {
		day             string
		relevant, total int
		top             string
	}
	var have []got
	for _, r := range rows {
		g := got{day: r.Date.Format(time.DateOnly), relevant: r.Relevant, total: r.Total}
		if r.HasTop {
			g.top = r.Top.ID
		}
		have = append(have, g)
	}
	want := []got{
		{"2026-11-02", 1, 1, "late-nov2"},
		{"2026-11-03", 1, 2, "early-nov3"},
		{"2026-11-04", 1, 1, "early-nov4"},
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("rows = %v, want %v", have, want)
	}
}

func TestDayRowsKeepEveryDayAcrossDaylightSavingEnd(t *testing.T) {
	// Berlin leaves summer time on 2026-10-25, a 25-hour day.
	p := NewParams(time.Date(2026, 11, 4, 12, 0, 0, 0, berlin), 12, 10, 0.5, berlin)
	rows := DayRows(nil, p)
	if len(rows) != 12 {
		t.Fatalf("%d days, want 12", len(rows))
	}
	for i, r := range rows {
		if h, m, s := r.Date.Clock(); h != 0 || m != 0 || s != 0 {
			t.Errorf("row %d starts at %v, want midnight", i, r.Date)
		}
		if r.Total != 0 || r.HasTop {
			t.Errorf("row %d of an empty page is not empty: %+v", i, r)
		}
	}
	if first := rows[0].Date.Format(time.DateOnly); first != "2026-10-24" {
		t.Errorf("first day %s, want 2026-10-24", first)
	}
}

func TestTopEventOfDay(t *testing.T) {
	p := NewParams(utc(3, 12, 0), 1, 10, 0.5, time.UTC)
	arts := []model.Article{
		pageArticle("low", utc(3, 9, 0), 0.9, 3, model.EventResults, nil, nil),
		pageArticle("high", utc(3, 8, 0), 0.6, 4, model.EventBuyback, nil, nil),
		pageArticle("tie-older", utc(3, 7, 0), 0.6, 4, model.EventBuyback, nil, nil),
		pageArticle("below-cut", utc(3, 10, 0), 0.49, 5, model.EventOther, nil, nil),
	}
	rows := DayRows(arts, p)
	if len(rows) != 1 || rows[0].Top.ID != "high" || rows[0].Relevant != 3 || rows[0].Total != 4 {
		t.Errorf("rows = %+v, want top high with 3 of 4 relevant", rows)
	}
}

func TestStockPageRelevanceCutoffAndEmpty(t *testing.T) {
	p := NewParams(utc(3, 12, 0), 30, 10, 0.5, berlin)
	arts := []model.Article{
		pageArticle("at", utc(3, 8, 0), 0.5, 3, model.EventBuyback, []string{"ACME"}, []string{"ai"}),
		pageArticle("below", utc(3, 7, 0), 0.49, 5, model.EventOther, []string{"acme"}, []string{"chatter"}),
		pageArticle("other-stock", utc(3, 6, 0), 0.9, 5, model.EventResults, []string{"GLOB"}, nil),
		pageArticle("old", time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC), 0.9, 5, model.EventResults, []string{"ACME"}, nil),
	}
	tests := []struct {
		name         string
		arts         []model.Article
		ticker       string
		total, relev int
		newest       []string
		kinds        []CountRow
	}{
		{"cut-off is inclusive", arts, "ACME", 2, 1, []string{"at"}, []CountRow{{"buyback", 1}}},
		{"case is ignored", arts, "acme", 2, 1, []string{"at"}, []CountRow{{"buyback", 1}}},
		{"unknown ticker", arts, "NOPE", 0, 0, nil, nil},
		{"no articles at all", nil, "ACME", 0, 0, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pg := BuildStockPage(tt.arts, tt.ticker, "", p)
			if pg.Total != tt.total || pg.Relevant != tt.relev {
				t.Errorf("total %d relevant %d, want %d and %d", pg.Total, pg.Relevant, tt.total, tt.relev)
			}
			var ids []string
			for _, a := range pg.Newest {
				ids = append(ids, a.ID)
			}
			if !reflect.DeepEqual(ids, tt.newest) {
				t.Errorf("newest = %v, want %v", ids, tt.newest)
			}
			if !reflect.DeepEqual(pg.Kinds, tt.kinds) {
				t.Errorf("kinds = %v, want %v", pg.Kinds, tt.kinds)
			}
		})
	}

	pg := BuildStockPage(arts, "ACME", "", p)
	if len(pg.Topics) != 1 || pg.Topics[0].Text != "ai" {
		t.Errorf("related topics = %v, want only ai (the irrelevant article's topic is left out)", pg.Topics)
	}
}

func TestWeekRowsStartOnMonday(t *testing.T) {
	arts := []model.Article{
		// Sunday 2026-11-01 23:30 in Berlin is 22:30 UTC.
		{ID: "sun", Published: utc(1, 22, 30), Score: model.Score{Materiality: 2, Tone: -0.5}},
		// Monday 2026-11-02 00:30 in Berlin.
		{ID: "mon1", Published: utc(1, 23, 30), Score: model.Score{Materiality: 4, Tone: 0.5}},
		{ID: "mon2", Published: utc(5, 12, 0), Score: model.Score{Materiality: 5, Tone: 0.1}},
	}
	rows := WeekRows(arts, berlin)
	if len(rows) != 2 {
		t.Fatalf("%d weeks, want 2: %+v", len(rows), rows)
	}
	if got := rows[0].Week.Format(time.DateOnly); got != "2026-10-26" || rows[0].Articles != 1 || rows[0].Materiality != 2 || rows[0].Tone != -0.5 {
		t.Errorf("first week = %+v", rows[0])
	}
	if got := rows[1].Week.Format(time.DateOnly); got != "2026-11-02" || rows[1].Articles != 2 || rows[1].Materiality != 4.5 || rows[1].Tone < 0.299 || rows[1].Tone > 0.301 {
		t.Errorf("second week = %+v", rows[1])
	}
}

func TestTopicPage(t *testing.T) {
	p := NewParams(utc(3, 12, 0), 30, 2, 0.5, berlin)
	arts := []model.Article{
		pageArticle("a", utc(3, 8, 0), 0.8, 4, model.EventBuyback, []string{"ACME", "GLOB"}, []string{"ai", "capex"}),
		pageArticle("b", utc(2, 8, 0), 0.7, 5, model.EventResults, []string{"ACME"}, []string{"AI"}),
		pageArticle("c", utc(1, 8, 0), 0.9, 3, model.EventResults, []string{"INIT"}, []string{"ai", "capex"}),
		pageArticle("weak", utc(1, 9, 0), 0.1, 1, model.EventOther, []string{"WEAK"}, []string{"ai"}),
		pageArticle("elsewhere", utc(1, 9, 0), 0.9, 5, model.EventOther, []string{"ELSE"}, []string{"energy"}),
	}
	pg := BuildTopicPage(arts, "ai", p)
	if pg.Total != 4 || pg.Relevant != 3 {
		t.Errorf("total %d relevant %d, want 4 and 3", pg.Total, pg.Relevant)
	}
	if got := termsOf(pg.Stocks); !reflect.DeepEqual(got, []string{"ACME:2", "GLOB:1", "INIT:1"}) {
		t.Errorf("stocks = %v", got)
	}
	if got := termsOf(pg.Related); !reflect.DeepEqual(got, []string{"capex:2"}) {
		t.Errorf("related = %v, want capex only (the topic itself is left out, in any case)", got)
	}
	var top []string
	for _, a := range pg.Top {
		top = append(top, a.ID)
	}
	if !reflect.DeepEqual(top, []string{"b", "a"}) {
		t.Errorf("top = %v, want b then a (materiality, limited to 2)", top)
	}

	empty := BuildTopicPage(arts, "nothing", p)
	if empty.Total != 0 || len(empty.Top) != 0 || len(empty.Days) != 30 {
		t.Errorf("empty topic page = %+v", empty)
	}
}

func termsOf(terms []tagcloud.Term) []string {
	var out []string
	for _, t := range terms {
		out = append(out, t.Text+":"+string(rune('0'+t.Count)))
	}
	return out
}

func TestTopicsPageLift(t *testing.T) {
	p := NewParams(utc(14, 12, 0), 7, 10, 0.5, time.UTC) // Nov 8 to Nov 14; before: Nov 1 to Nov 7
	arts := []model.Article{
		pageArticle("n1", utc(10, 8, 0), 0.8, 3, "", nil, []string{"ai", "new"}),
		pageArticle("n2", utc(11, 8, 0), 0.8, 3, "", nil, []string{"ai", "new"}),
		pageArticle("n3", utc(12, 8, 0), 0.8, 3, "", nil, []string{"ai"}),
		pageArticle("weak", utc(12, 9, 0), 0.1, 3, "", nil, []string{"noise"}),
		pageArticle("p1", utc(3, 8, 0), 0.8, 3, "", nil, []string{"ai"}),
		pageArticle("p2", utc(4, 8, 0), 0.8, 3, "", nil, []string{"old"}),
		pageArticle("too-early", utc(30, 8, 0).AddDate(0, -1, 0), 0.8, 3, "", nil, []string{"ai"}),
	}
	pg := BuildTopicsPage(arts, p)
	if pg.Total != 4 || pg.Relevant != 3 {
		t.Errorf("total %d relevant %d, want 4 and 3", pg.Total, pg.Relevant)
	}
	want := []TopicRow{
		{Topic: "ai", Count: 3, Prior: 1, Lift: 3},
		{Topic: "new", Count: 2, Prior: 0, Lift: 4},
	}
	if len(pg.Rows) != len(want) {
		t.Fatalf("rows = %+v", pg.Rows)
	}
	for i, w := range want {
		g := pg.Rows[i]
		if g.Topic != w.Topic || g.Count != w.Count || g.Prior != w.Prior || g.Lift != w.Lift {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestStocksPage(t *testing.T) {
	p := NewParams(utc(3, 12, 0), 14, 10, 0.5, time.UTC)
	mk := func(id string, mat int, tone float64, tickers ...string) model.Article {
		a := pageArticle(id, utc(2, 8, 0), 0.8, mat, "", tickers, nil)
		a.Score.Tone = tone
		return a
	}
	arts := []model.Article{
		mk("1", 4, 0.5, "ACME", "GLOB"),
		mk("2", 2, -0.1, "acme"),
		mk("3", 5, 0.2, "GLOB"),
		mk("4", 3, 0, "GLOB", "GLOB"),
		pageArticle("weak", utc(2, 9, 0), 0.2, 5, "", []string{"WEAK"}, nil),
	}
	pg := BuildStocksPage(arts, []string{"acme"}, p)
	if pg.Relevant != 4 || len(pg.Rows) != 2 {
		t.Fatalf("page = %+v", pg)
	}
	g, a := pg.Rows[0], pg.Rows[1]
	if g.Ticker != "GLOB" || g.Mentions != 3 || g.Materiality != 4 || g.Watched {
		t.Errorf("GLOB row = %+v", g)
	}
	if a.Ticker != "ACME" || a.Mentions != 2 || a.Materiality != 3 || a.Tone < 0.199 || a.Tone > 0.201 || !a.Watched {
		t.Errorf("ACME row = %+v", a)
	}
}

func TestClosestNames(t *testing.T) {
	known := []string{"ai-infra", "buybacks", "ai", "chips", "supply-chain", "chain"}
	tests := []struct {
		name string
		want []string
	}{
		{"ai", []string{"ai-infra", "chain", "supply-chain"}}, // prefix first, then substring; the exact name is not "similar"
		{"chain", []string{"supply-chain"}},                   // substring
		{"ai-infrastructure", []string{"ai-infra"}},           // the known name is contained in the query
		{"CHI", []string{"chips"}},                            // case is ignored
		{"zzz", []string{}},
	}
	for _, tt := range tests {
		got := ClosestNames(tt.name, known, 10)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ClosestNames(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
	if got := ClosestNames("c", known, 2); len(got) != 2 {
		t.Errorf("limit not applied: %v", got)
	}
}
