// Synthetic fixture, not verified against live data.

package discover

import (
	"reflect"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

var base = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// story is a relevant article published day days after base.
func story(id, cluster string, day, materiality int, tickers ...string) model.Article {
	return model.Article{
		ID:        id,
		ClusterID: cluster,
		Published: base.AddDate(0, 0, day),
		Tickers:   tickers,
		Score:     model.Score{Relevance: 0.9, Materiality: materiality},
	}
}

func tickersOf(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Ticker)
	}
	return out
}

func TestFindLeavesOutWatchlistAndIrrelevant(t *testing.T) {
	quiet := func(id string, day int, tickers ...string) model.Article {
		a := story(id, "", day, 5, tickers...)
		a.Score.Relevance = 0.1
		return a
	}
	articles := []model.Article{
		story("a1", "", 0, 3, "AAPL", "MSFT"),
		story("a2", "", 1, 3, "AAPL", "MSFT"),
		story("a3", "", 2, 3, "amzn"),
		story("a4", "", 3, 3, "AMZN"),
		quiet("n1", 0, "NVDA"),
		quiet("n2", 1, "NVDA"),
		quiet("n3", 2, "NVDA"),
	}
	got := Find(articles, []string{"aapl", " Amzn "}, Options{Threshold: 0.5})
	if want := []string{"MSFT"}; !reflect.DeepEqual(tickersOf(got), want) {
		t.Fatalf("tickers = %v, want %v", tickersOf(got), want)
	}
}

func TestFindCountsStoriesOnce(t *testing.T) {
	articles := []model.Article{
		story("a1", "c1", 0, 2, "XYZ"),
		story("a2", "c1", 1, 4, "XYZ"),
		story("a3", "c1", 2, 3, "XYZ"),
		story("a4", "", 3, 3, "XYZ", "XYZ"), // listed twice in one article
	}
	got := Find(articles, nil, Options{Threshold: 0.5, MinMentions: 1})
	if len(got) != 1 {
		t.Fatalf("candidates = %+v, want one", got)
	}
	c := got[0]
	if c.Mentions != 2 {
		t.Errorf("Mentions = %d, want 2 (cluster c1 counts once)", c.Mentions)
	}
	// The representative of c1 is the most material article, a2 with 4.
	if c.Weight != 4+3 {
		t.Errorf("Weight = %d, want 7", c.Weight)
	}
	if len(c.Articles) != 2 || c.Articles[0].ID != "a4" || c.Articles[1].ID != "a2" {
		t.Errorf("Articles = %v, want a4 then a2 (newest first)", ids(c.Articles))
	}
}

func TestFindRepresentativeTieGoesToNewest(t *testing.T) {
	articles := []model.Article{
		story("old", "c1", 0, 3, "XYZ"),
		story("new", "c1", 2, 3, "XYZ"),
	}
	got := Find(articles, nil, Options{Threshold: 0.5, MinMentions: 1})
	if len(got) != 1 || len(got[0].Articles) != 1 || got[0].Articles[0].ID != "new" {
		t.Fatalf("candidates = %+v, want the newest article as representative", got)
	}
}

func TestFindMinMentions(t *testing.T) {
	articles := []model.Article{story("a1", "", 0, 3, "ONE")}
	if got := Find(articles, nil, Options{Threshold: 0.5}); len(got) != 0 {
		t.Errorf("default MinMentions: got %v, want none (default is 2)", tickersOf(got))
	}
	if got := Find(articles, nil, Options{Threshold: 0.5, MinMentions: 1}); len(got) != 1 {
		t.Errorf("MinMentions 1: got %v, want ONE", tickersOf(got))
	}
}

func TestFindOrderAndLimit(t *testing.T) {
	articles := []model.Article{
		// AAA: weight 6, mentions 2
		story("aaa1", "", 0, 3, "AAA"), story("aaa2", "", 1, 3, "AAA"),
		// BBB: weight 5, mentions 3
		story("bbb1", "", 0, 2, "BBB"), story("bbb2", "", 1, 2, "BBB"), story("bbb3", "", 2, 1, "BBB"),
		// CCC and DDD: weight 4, mentions 2; CCC wins the ticker tie
		story("ccc1", "", 0, 2, "CCC"), story("ccc2", "", 1, 2, "CCC"),
		story("ddd1", "", 0, 2, "DDD"), story("ddd2", "", 1, 2, "DDD"),
		// EEE: weight 4, mentions 4; more mentions beats CCC and DDD
		story("eee1", "", 0, 1, "EEE"), story("eee2", "", 1, 1, "EEE"),
		story("eee3", "", 2, 1, "EEE"), story("eee4", "", 3, 1, "EEE"),
	}
	opts := Options{Threshold: 0.5}
	got := Find(articles, nil, opts)
	if want := []string{"AAA", "BBB", "EEE", "CCC", "DDD"}; !reflect.DeepEqual(tickersOf(got), want) {
		t.Fatalf("order = %v, want %v", tickersOf(got), want)
	}
	if got[0].Weight != 6 || got[0].Mentions != 2 {
		t.Errorf("AAA = weight %d mentions %d, want 6 and 2", got[0].Weight, got[0].Mentions)
	}

	opts.Limit = 2
	if limited := Find(articles, nil, opts); !reflect.DeepEqual(tickersOf(limited), []string{"AAA", "BBB"}) {
		t.Errorf("Limit 2 = %v, want AAA and BBB", tickersOf(limited))
	}
}

func TestFindArticlesNewestFirst(t *testing.T) {
	articles := []model.Article{
		story("a", "", 0, 1, "XYZ"),
		story("c", "", 2, 1, "XYZ"),
		story("b", "", 1, 1, "XYZ"),
	}
	got := Find(articles, nil, Options{Threshold: 0.5})
	if len(got) != 1 {
		t.Fatalf("candidates = %+v, want one", got)
	}
	if want := []string{"c", "b", "a"}; !reflect.DeepEqual(ids(got[0].Articles), want) {
		t.Errorf("Articles = %v, want %v", ids(got[0].Articles), want)
	}
}

func TestFindEmpty(t *testing.T) {
	if got := Find(nil, nil, Options{Threshold: 0.5}); len(got) != 0 {
		t.Fatalf("Find(nil) = %+v, want none", got)
	}
}

func ids(as []model.Article) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func TestATickerOnlyListedInARoundupIsNotAStoryAboutIt(t *testing.T) {
	roundup := func(id string) model.Article {
		return model.Article{ID: id, Title: "Acme beats estimates", Tickers: []string{"ACME", "XYZ", "AAA", "BBB"},
			Score: model.Score{Relevance: 0.9, Materiality: 4}}
	}
	got := Find([]model.Article{roundup("1"), roundup("2")}, nil, Options{Threshold: 0.5})
	for _, c := range got {
		if c.Ticker == "XYZ" {
			t.Errorf("XYZ, only listed in two roundups, became a candidate: %+v", c)
		}
	}
	named := model.Article{ID: "3", Title: "XYZ wins contract", Tickers: []string{"ACME", "XYZ", "AAA", "BBB"},
		Score: model.Score{Relevance: 0.9, Materiality: 4}}
	named2 := named
	named2.ID = "4"
	got = Find([]model.Article{named, named2}, nil, Options{Threshold: 0.5})
	found := false
	for _, c := range got {
		found = found || c.Ticker == "XYZ"
	}
	if !found {
		t.Error("XYZ, named in the titles, should be a candidate")
	}
}
