package main

import (
	"bytes"
	"github.com/b070nd/stoxicity/app/src/pages"
	"strings"
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

func TestRenderStockPageEmptyAndIrrelevant(t *testing.T) {
	p := pages.NewParams(utc(3, 12, 0), 30, 10, 0.5, berlin)
	var b bytes.Buffer
	renderStockPage(&b, pages.BuildStockPage(nil, "NOPE", "", p), "default")
	out := b.String()
	for _, want := range []string{"Stock NOPE, tenant default", "price and fundamentals: not available (no market data provider is configured)", "No articles name NOPE"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Timeline") {
		t.Errorf("an empty page has a timeline:\n%s", out)
	}

	b.Reset()
	weak := []model.Article{pageArticle("w", utc(3, 8, 0), 0.1, 1, model.EventOther, []string{"ACME"}, nil)}
	renderStockPage(&b, pages.BuildStockPage(weak, "ACME", "Acme Corp", p), "default")
	out = b.String()
	for _, want := range []string{"Stock ACME - Acme Corp", "1 articles name ACME, 0 relevant", "No article reaches the relevance threshold", "Timeline"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Newest relevant") {
		t.Errorf("a page without relevant articles lists some:\n%s", out)
	}
}

func TestRenderCloud(t *testing.T) {
	terms := []tagcloud.Term{{Text: "ai", Count: 9, Tier: 5}, {Text: "chips", Count: 5, Tier: 4}, {Text: "tax", Count: 3, Tier: 3}, {Text: "oil", Count: 2, Tier: 2}, {Text: "fx", Count: 1, Tier: 1}}
	var b bytes.Buffer
	renderCloud(&b, terms)
	want := "  ****AI****(9)  ***CHIPS***(5)  **tax**(3)  *oil*(2)  fx(1)\n"
	if !strings.HasPrefix(b.String(), want) {
		t.Errorf("cloud =\n%s\nwant it to start with\n%s", b.String(), want)
	}

	var many []tagcloud.Term
	for i := 0; i < 40; i++ {
		many = append(many, tagcloud.Term{Text: "topic-" + string(rune('a'+i%26)), Count: 3, Tier: 3})
	}
	b.Reset()
	renderCloud(&b, many)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Errorf("40 terms fit in %d lines", len(lines))
	}
	for _, l := range lines {
		if len(l) > cloudWidth {
			t.Errorf("line of %d characters: %q", len(l), l)
		}
	}
}

func TestRenderTopicsPageShowsTheCloud(t *testing.T) {
	p := pages.NewParams(utc(14, 12, 0), 7, 10, 0.5, time.UTC)
	arts := []model.Article{
		pageArticle("n1", utc(10, 8, 0), 0.8, 3, "", nil, []string{"ai", "new"}),
		pageArticle("n2", utc(11, 8, 0), 0.8, 3, "", nil, []string{"ai", "new"}),
		pageArticle("n3", utc(12, 8, 0), 0.8, 3, "", nil, []string{"ai"}),
	}
	var b bytes.Buffer
	renderTopicsPage(&b, pages.BuildTopicsPage(arts, p), "default")
	if !strings.Contains(b.String(), "****AI****(3)") {
		t.Errorf("cloud missing:\n%s", b.String())
	}
}
