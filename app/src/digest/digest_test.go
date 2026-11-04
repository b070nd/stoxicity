// Synthetic fixture, not verified against live data.

package digest

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// at is a relevant article published at when, with its domain derived from id.
func at(id string, when time.Time, mat int, title string, tickers ...string) model.Article {
	return model.Article{
		ID:        id,
		Domain:    id + ".com",
		Title:     title,
		Published: when,
		Tickers:   tickers,
		Score:     model.Score{Relevance: 0.9, Materiality: mat},
	}
}

func utc(day, hour int) time.Time {
	return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC)
}

func ids(as []model.Article) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

var opts = Options{Threshold: 0.5}

func TestBuildDayBoundaryFollowsLocation(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	articles := []model.Article{
		at("A", time.Date(2026, 11, 3, 23, 30, 0, 0, time.UTC), 3, ""), // 00:30 on 4 Nov in Berlin
		at("B", utc(4, 10), 3, ""),
		at("C", time.Date(2026, 11, 4, 23, 30, 0, 0, time.UTC), 3, ""), // 00:30 on 5 Nov in Berlin
	}
	day := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		loc     *time.Location
		wantDay time.Time
		wantTop []string
	}{
		{
			name:    "Berlin",
			loc:     berlin,
			wantDay: time.Date(2026, 11, 4, 0, 0, 0, 0, berlin),
			wantTop: []string{"B", "A"},
		},
		{
			name:    "UTC",
			loc:     time.UTC,
			wantDay: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
			wantTop: []string{"C", "B"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Build(day, c.loc, articles, nil, nil, opts)
			if !got.Day.Equal(c.wantDay) {
				t.Errorf("Day = %v, want %v", got.Day, c.wantDay)
			}
			if !reflect.DeepEqual(ids(got.Top), c.wantTop) {
				t.Errorf("Top = %v, want %v", ids(got.Top), c.wantTop)
			}
		})
	}
}

func TestBuildNilLocationIsUTC(t *testing.T) {
	articles := []model.Article{
		at("X", time.Date(2026, 11, 4, 0, 30, 0, 0, time.UTC), 3, ""),
		at("Y", time.Date(2026, 11, 3, 23, 30, 0, 0, time.UTC), 3, ""),
	}
	got := Build(utc(4, 12), nil, articles, nil, nil, opts)
	if want := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC); !got.Day.Equal(want) {
		t.Errorf("Day = %v, want %v", got.Day, want)
	}
	if want := []string{"X"}; !reflect.DeepEqual(ids(got.Top), want) {
		t.Errorf("Top = %v, want %v", ids(got.Top), want)
	}
}

func TestBuildTopCollapsesClusterToEarliest(t *testing.T) {
	cluster := func(a model.Article, c string) model.Article {
		a.ClusterID = c
		return a
	}
	articles := []model.Article{
		cluster(at("late", utc(4, 15), 5, ""), "c1"),
		cluster(at("early", utc(4, 8), 3, ""), "c1"),
		cluster(at("middle", utc(4, 11), 4, ""), "c1"),
		at("solo", utc(4, 9), 2, ""),
	}
	got := Build(utc(4, 12), nil, articles, nil, nil, opts)
	// The cluster is represented by its earliest article, which has materiality
	// 3, so it sorts above the standalone article with materiality 2.
	if want := []string{"early", "solo"}; !reflect.DeepEqual(ids(got.Top), want) {
		t.Errorf("Top = %v, want %v", ids(got.Top), want)
	}
}

func TestBuildTopOrderAndTopN(t *testing.T) {
	articles := []model.Article{
		at("x", utc(4, 9), 3, ""),
		at("y", utc(4, 9), 5, ""),
		at("z", utc(4, 8), 5, ""),
		at("w", utc(4, 9), 1, ""),
	}
	articles[1].Score.Relevance = 0.6 // same materiality as z, less relevant
	got := Build(utc(4, 12), nil, articles, nil, nil, Options{Threshold: 0.5, TopN: 2})
	// z and y tie on materiality; z is more relevant. TopN keeps two.
	if want := []string{"z", "y"}; !reflect.DeepEqual(ids(got.Top), want) {
		t.Errorf("Top = %v, want %v", ids(got.Top), want)
	}
}

func TestBuildIrrelevantAndOtherDaysAreExcludedFromTop(t *testing.T) {
	irrelevant := at("quiet", utc(4, 9), 5, "")
	irrelevant.Score.Relevance = 0.1
	articles := []model.Article{
		irrelevant,
		at("yesterday", utc(3, 9), 5, ""),
		at("today", utc(4, 9), 1, ""),
	}
	got := Build(utc(4, 12), nil, articles, nil, nil, opts)
	if want := []string{"today"}; !reflect.DeepEqual(ids(got.Top), want) {
		t.Errorf("Top = %v, want %v", ids(got.Top), want)
	}
}

func TestBuildCandidatesTrendsDomainsAndSummary(t *testing.T) {
	cloud := []string{"cloud"}
	articles := []model.Article{
		at("t1", utc(4, 9), 3, "Microsoft cloud revenue grew across enterprise customers this quarter.", "MSFT"),
		at("t2", utc(4, 10), 3, "Microsoft announced a new Azure data centre in Ireland.", "msft"),
		at("t3", utc(4, 11), 2, "Apple shares moved on a supply report today.", "AAPL"),
	}
	for i := range articles {
		articles[i].Topics = cloud
	}
	var baseline []model.Article
	for i := 0; i < 14; i++ {
		b := at("b", utc(1+i%3, 9), 1, "")
		b.Topics = cloud
		baseline = append(baseline, b)
	}

	got := Build(utc(4, 12), nil, articles, baseline, []string{"aapl"}, opts)

	if len(got.Candidates) != 1 || got.Candidates[0].Ticker != "MSFT" || got.Candidates[0].Mentions != 2 {
		t.Errorf("Candidates = %+v, want MSFT with 2 mentions and AAPL left out", got.Candidates)
	}
	if len(got.Trends) != 1 || got.Trends[0].Topic != "cloud" || got.Trends[0].Today != 3 {
		t.Fatalf("Trends = %+v, want cloud with 3 today", got.Trends)
	}
	// Baseline is 14 articles over the default 14 days, so 1 a day and lift 3.
	if got.Trends[0].Baseline != 1 || got.Trends[0].Lift != 3 {
		t.Errorf("trend = %+v, want baseline 1 and lift 3", got.Trends[0])
	}
	if len(got.Domains) != 3 {
		t.Errorf("Domains = %+v, want three", got.Domains)
	}
	if len(got.Summary.Lines) != 3 {
		t.Errorf("Summary lines = %d, want 3", len(got.Summary.Lines))
	}
}

func TestBuildSummaryDefaultsToFiveLines(t *testing.T) {
	titles := []string{
		"Lumen Corp raised its buyback authorisation to two billion dollars.",
		"Harbor Freight disclosed a cyber incident affecting payment systems.",
		"Verdant Energy named a new chief financial officer effective January.",
		"Quartz Motors recalled forty thousand vehicles over brake software.",
		"Pioneer Bank agreed to acquire a regional lender for cash.",
		"Solaris Labs delayed its phase three trial results into next year.",
		"Granite Mining cut its annual production guidance by twelve percent.",
	}
	var articles []model.Article
	for i, title := range titles {
		articles = append(articles, at(string(rune('a'+i)), utc(4, 8+i), 3, title))
	}
	got := Build(utc(4, 12), nil, articles, nil, nil, opts)
	if len(got.Top) != 7 {
		t.Fatalf("Top has %d articles, want 7", len(got.Top))
	}
	if len(got.Summary.Lines) != 5 {
		t.Errorf("Summary lines = %d, want 5", len(got.Summary.Lines))
	}
}

func TestBuildEmpty(t *testing.T) {
	day := utc(4, 12)
	got := Build(day, nil, nil, nil, nil, Options{})
	if !got.Day.Equal(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Day = %v, want midnight on 4 November", got.Day)
	}
	if len(got.Top) != 0 || len(got.Domains) != 0 || len(got.Candidates) != 0 ||
		len(got.Trends) != 0 || len(got.Summary.Lines) != 0 {
		t.Errorf("Build(empty) = %+v, want no content", got)
	}
}

func TestBuildWindowCoversASpanAcrossMidnight(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	to := time.Date(2026, 11, 4, 7, 30, 0, 0, berlin)
	from := to.Add(-24 * time.Hour)
	articles := []model.Article{
		at("early", from.Add(-time.Minute), 5, ""), // just before the span
		at("evening", time.Date(2026, 11, 3, 21, 0, 0, 0, berlin), 4, ""),
		at("morning", time.Date(2026, 11, 4, 6, 0, 0, 0, berlin), 3, ""),
		at("later", to, 5, ""), // the end is exclusive
	}
	got := BuildWindow(from, to, berlin, articles, nil, nil, opts)
	if want := time.Date(2026, 11, 3, 0, 0, 0, 0, berlin); !got.Day.Equal(want) || !got.Until.Equal(to) {
		t.Errorf("Day = %v, Until = %v; want %v and %v", got.Day, got.Until, want, to)
	}
	if want := []string{"evening", "morning"}; !reflect.DeepEqual(ids(got.Top), want) {
		t.Errorf("Top = %v, want %v", ids(got.Top), want)
	}
}

func TestTextHeading(t *testing.T) {
	loc := time.UTC
	day := Digest{Day: time.Date(2026, 11, 3, 0, 0, 0, 0, loc)}
	var b strings.Builder
	Text(&b, day, "alice", loc, 0, 0.5)
	if want := "Digest for 2026-11-03 (UTC), tenant alice\n0 articles, 0 relevant (relevance at least 0.50)\n"; b.String() != want {
		t.Errorf("single day:\n%q\nwant\n%q", b.String(), want)
	}
	span := Digest{Day: day.Day, Until: time.Date(2026, 11, 10, 7, 30, 0, 0, loc)}
	b.Reset()
	Text(&b, span, "alice", loc, 0, 0.5)
	if want := "Digest for 2026-11-03 to 2026-11-10 (UTC), tenant alice\n"; !strings.HasPrefix(b.String(), want) {
		t.Errorf("span heading:\n%q\nwant prefix %q", b.String(), want)
	}
}

func TestTextSections(t *testing.T) {
	a := at("sec", utc(3, 9), 4, "  Acme\nbuyback  ")
	a.URL = "https://sec.example/acme"
	a.Score.Kind = model.EventBuyback
	dg := Build(utc(3, 12), time.UTC, []model.Article{a}, nil, nil, opts)
	var b strings.Builder
	Text(&b, dg, "alice", time.UTC, 1, 0.5)
	out := b.String()
	for _, want := range []string{
		"\nTop\n",
		"  1. 09:00  buyback  materiality 4  relevance 0.90\n",
		"     Acme buyback\n     sec.com  https://sec.example/acme\n",
		"\nDomains (relevant / total)\n  sec.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	a.Score.Kind = ""
	b.Reset()
	Text(&b, Build(utc(3, 12), time.UTC, []model.Article{a}, nil, nil, opts), "alice", time.UTC, 1, 0.5)
	if !strings.Contains(b.String(), "  1. 09:00  -  materiality 4") {
		t.Errorf("an article without a kind shows %q:\n%s", "-", b.String())
	}
}
