package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// scoredArticle is a synthetic stored article of a source.
func scoredArticle(id, source, domain string, published time.Time, relevance float64, materiality int, kind model.EventKind, tickers, topics []string) model.Article {
	return model.Article{
		ID: id, SourceID: source, Domain: domain, URL: "https://" + domain + "/" + id,
		Title: "Headline of " + id + " about the business", Summary: "Summary of " + id + ".",
		Published: published, Tickers: tickers, Topics: topics,
		Score: model.Score{Relevance: relevance, Materiality: materiality, Kind: kind, LensVersion: "v1"},
	}
}

// savePageArticles stores arts in the database of the test environment.
func savePageArticles(t *testing.T, e *testEnv, arts ...model.Article) {
	t.Helper()
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Save(context.Background(), arts); err != nil {
		t.Fatal(err)
	}
}

// addOtherTenant adds a personal source owned by the tenant "other", which
// the default tenant must never see, and the tenant itself.
func addOtherTenant(t *testing.T, e *testEnv) {
	t.Helper()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(e.dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sources/private.yaml", fmt.Sprintf(`sources:
  - id: private-feed
    name: Someone else's feed
    domain: private.example.com
    kind: rss
    url: %s/private.xml
    tier: 2
    licence: personal
    owner: other
    enabled: true
`, e.server.URL))
	write("tenants/other/tenant.yaml", "id: other\nname: Other\n")
}

// The clock is 2026-11-03 11:00 in Berlin. Berlin is UTC+1.
func seedStockData(t *testing.T, e *testEnv) {
	t.Helper()
	addOtherTenant(t, e)
	const pub, other = "news-feed", "private-feed"
	savePageArticles(t, e,
		scoredArticle("a1", pub, "news.example.com", utc(3, 8, 0), 0.8, 4, model.EventBuyback, []string{"ACME"}, []string{"buybacks", "ai"}),
		// 00:30 on Nov 3 in Berlin, 23:30 on Nov 2 in UTC.
		scoredArticle("a2", pub, "news.example.com", utc(2, 23, 30), 0.6, 3, model.EventResults, []string{"ACME"}, []string{"ai"}),
		scoredArticle("a3", pub, "news.example.com", utc(1, 12, 0), 0.3, 1, model.EventPriceChatter, []string{"ACME"}, nil),
		scoredArticle("a4", pub, "other.example.com", utc(1, 10, 0), 0.9, 5, model.EventOther, []string{"GLOB"}, []string{"ai"}),
		scoredArticle("p1", other, "private.example.com", utc(3, 7, 0), 0.95, 5, model.EventResults, []string{"ACME", "HIDDEN"}, []string{"ai", "secretsauce"}),
	)
}

func TestStockCommand(t *testing.T) {
	e := newTestEnv(t)
	seedStockData(t, e)

	r := e.run(t, "stock", "acme")
	r.wantCode(t, exitOK)
	r.wantOut(t,
		"Stock ACME, tenant default",
		"Window 2026-10-05 to 2026-11-03 (30 days, Europe/Berlin)",
		"price and fundamentals: not available (no market data provider is configured)",
		"3 articles name ACME, 2 relevant (relevance at least 0.50)",
		"Summary",
		"Headline of a1 about the business (news.example.com)",
		"2026-11-03 Tue  2 / 2", // a2 counts on Nov 3 in Berlin
		"2026-11-02 Mon  0 / 0",
		"2026-11-01 Sun  0 / 1",
		"09:00 buyback", // the top event of Nov 3 is the more material a1, 08:00 UTC
		"week of 2026-11-02  2 articles  materiality 3.5  tone +0.00",
		"buyback", "results",
		"news.example.com",
		"****AI****(2)", "buybacks(1)",
		"Newest relevant articles",
		"https://news.example.com/a1",
	)
	r.wantNotOut(t, "private", "secret", "HIDDEN", "p1", "a3 about", "Headline of a4")
	if r.errOut != "" {
		t.Errorf("stderr: %s", r.errOut)
	}

	r = e.run(t, "stock", "ACME", "-limit", "1", "-days", "3")
	r.wantCode(t, exitOK)
	r.wantOut(t, "(3 days, Europe/Berlin)", "Headline of a1")
	if n := strings.Count(r.out, "https://news.example.com/a"); n != 1 {
		t.Errorf("-limit 1 listed %d articles:\n%s", n, r.out)
	}

	// The owner of the personal source sees what the default tenant does not.
	r = e.run(t, "-tenant", "other", "stock", "ACME")
	r.wantCode(t, exitOK)
	r.wantOut(t, "4 articles name ACME, 3 relevant", "private.example.com", "secretsauce(1)")
}

func TestStockCommandUnknownTicker(t *testing.T) {
	e := newTestEnv(t)
	seedStockData(t, e)
	r := e.run(t, "stock", "NOPE")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Stock NOPE", "price and fundamentals: not available", "No articles name NOPE in this window.")
	r.wantNotOut(t, "Timeline")

	// A ticker only the other tenant's source names is unknown to this one.
	r = e.run(t, "stock", "HIDDEN")
	r.wantCode(t, exitOK)
	r.wantOut(t, "No articles name HIDDEN")
	r.wantNotOut(t, "private")
}

func TestStocksCommand(t *testing.T) {
	e := newTestEnv(t)
	seedStockData(t, e)
	r := e.run(t, "stocks")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Stocks, tenant default", "4 articles, 3 relevant")
	if line := lineStarting(r.out, "ACME"); !strings.Contains(line, "2") || !strings.Contains(line, "3.5") || !strings.Contains(line, "(watchlist)") {
		t.Errorf("ACME line = %q", line)
	}
	if line := lineStarting(r.out, "GLOB"); !strings.Contains(line, "5.0") || strings.Contains(line, "watchlist") {
		t.Errorf("GLOB line = %q", line)
	}
	r.wantNotOut(t, "HIDDEN", "private")
}

func TestTopicCommand(t *testing.T) {
	e := newTestEnv(t)
	seedStockData(t, e)

	r := e.run(t, "topic", "ai")
	r.wantCode(t, exitOK)
	r.wantOut(t,
		"Topic ai, tenant default",
		"3 articles, 3 relevant",
		"Stocks (by relevant articles that name them)",
		"ACME", "GLOB",
		"other.example.com",
		"Timeline",
		"Related topics", "****BUYBACKS****(1)", // a cloud of one term is in the top tier
		"Top articles",
		"https://other.example.com/a4",
	)
	r.wantNotOut(t, "private", "secret", "HIDDEN")
	// The most material article leads the list.
	i4, i1, i2 := strings.Index(r.out, "https://other.example.com/a4"), strings.Index(r.out, "https://news.example.com/a1"), strings.Index(r.out, "https://news.example.com/a2")
	if i4 > i1 || i1 > i2 {
		t.Errorf("articles are not ordered by materiality:\n%s", r.out)
	}
}

func TestTopicCommandUnknownListsClosest(t *testing.T) {
	e := newTestEnv(t)
	seedStockData(t, e)

	r := e.run(t, "topic", "buy")
	r.wantCode(t, exitOK)
	r.wantOut(t, `Unknown topic "buy"`, "Closest topics:", "  buybacks")
	r.wantNotOut(t, "Topic buy,")

	// A topic only the other tenant's source carries is unknown here, and is
	// not offered as a close match either.
	r = e.run(t, "topic", "secret")
	r.wantCode(t, exitOK)
	r.wantOut(t, `Unknown topic "secret"`, "No topic name is similar")
	r.wantNotOut(t, "secretsauce")
	r = e.run(t, "topic", "secretsauce")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Unknown topic")
	r.wantNotOut(t, "private.example.com")

	r = e.run(t, "-tenant", "other", "topic", "secretsauce")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Topic secretsauce", "1 articles, 1 relevant", "HIDDEN")
}

func TestTopicsCommand(t *testing.T) {
	e := newTestEnv(t)
	seedStockData(t, e)
	// One more article of topic ai in the 14 days before the window.
	savePageArticles(t, e, scoredArticle("old", "news-feed", "news.example.com", time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC), 0.7, 3, model.EventResults, []string{"ACME"}, []string{"ai"}))

	r := e.run(t, "topics")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Topics, tenant default", "Cloud", "****AI****(3)", "buybacks(1)")
	if f := strings.Fields(lineStarting(r.out, "ai ")); len(f) != 4 || f[1] != "3" || f[2] != "1" || f[3] != "3.0" {
		t.Errorf("ai row = %v, want 3 now, 1 before, lift 3.0", f)
	}
	if f := strings.Fields(lineStarting(r.out, "buybacks ")); len(f) != 4 || f[1] != "1" || f[2] != "0" || f[3] != "2.0" {
		t.Errorf("buybacks row = %v, want 1 now, 0 before, lift 2.0", f)
	}
	r.wantNotOut(t, "secret")
}

// lineStarting returns the first line of out that starts with prefix.
func lineStarting(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func TestPageCommandsUsageErrors(t *testing.T) {
	e := newTestEnv(t)
	tests := []struct {
		name string
		args []string
	}{
		{"stock without ticker", []string{"stock"}},
		{"stock two tickers", []string{"stock", "A", "B"}},
		{"stock bad ticker", []string{"stock", "../x"}},
		{"stock zero days", []string{"stock", "ACME", "-days", "0"}},
		{"stock zero limit", []string{"stock", "ACME", "-limit", "0"}},
		{"stocks extra", []string{"stocks", "now"}},
		{"stocks too many days", []string{"stocks", "-days", "100000"}},
		{"topic without name", []string{"topic"}},
		{"topic blank name", []string{"topic", " "}},
		{"topics extra", []string{"topics", "ai"}},
		{"label without subcommand", []string{"label"}},
		{"label unknown subcommand", []string{"label", "maybe", "x"}},
		{"label keep without ID", []string{"label", "keep"}},
		{"label list zero", []string{"label", "list", "-n", "0"}},
		{"label sample extra", []string{"label", "sample", "x"}},
		{"eval extra", []string{"eval", "x"}},
		{"eval bad threshold", []string{"eval", "-threshold", "2"}},
		{"rescore days and range", []string{"rescore", "-days", "5", "-from", "2026-11-01", "-to", "2026-11-02"}},
		{"rescore from alone", []string{"rescore", "-from", "2026-11-01"}},
		{"rescore reversed", []string{"rescore", "-from", "2026-11-03", "-to", "2026-11-01"}},
		{"rescore bad day", []string{"rescore", "-from", "x", "-to", "2026-11-01"}},
		{"rescore zero days", []string{"rescore", "-days", "0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.run(t, tt.args...)
			r.wantCode(t, exitUsage)
			if !strings.Contains(r.errOut, "usage: stoxicity") {
				t.Errorf("stderr has no usage line:\n%s", r.errOut)
			}
			if r.out != "" {
				t.Errorf("stdout not empty:\n%s", r.out)
			}
		})
	}
}
