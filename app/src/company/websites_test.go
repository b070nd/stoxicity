package company

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

const sitesTest = "https://sites.test/sparql"

// bindings writes Wikidata's answer for rows of ticker, label, site, rank.
func bindings(rows ...[4]string) string {
	var b []string
	for _, r := range rows {
		b = append(b, fmt.Sprintf(`{"t":{"value":%q},"label":{"value":%q},"site":{"value":%q},"rank":{"value":"http://wikiba.se/ontology#%s"}}`, r[0], r[1], r[2], r[3]))
	}
	return `{"results":{"bindings":[` + strings.Join(b, ",") + `]}}`
}

func TestSameCompany(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Apple Inc.", "Apple Inc.", true},
		{"COCA COLA CO", "The Coca-Cola Company", true},
		{"NVIDIA CORP", "Nvidia", true},
		{"Alphabet Inc.", "Alphabet", true},
		{"Agilent Technologies, Inc.", "Agent Orange Ltd", false},
		{"A", "Apple", false}, // too short to be a prefix
		{"", "Apple", false},
		{"Inc.", "Corp", false},
	} {
		if got := SameCompany(c.a, c.b); got != c.want {
			t.Errorf("SameCompany(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestEnrichWebsitesPicksTheRightCompanyAndTheBestAddress(t *testing.T) {
	dir := t.TempDir()
	targets := []SiteTarget{{"AAPL", "Apple Inc."}, {"AWCA", "Awaysis Capital, Inc."}, {"A", "Agilent Technologies, Inc."}, {"KO", "Coca-Cola Co"}}
	f := httpx.NewFake()
	// "A" is also the ticker of another company; AWCA is not on Wikidata; Apple
	// has a site for every country, and the plain one is the shortest.
	f.Reply(sitesRequestURL(sitesTest, []SiteTarget{{"A", ""}, {"AAPL", ""}, {"AWCA", ""}, {"KO", ""}}), resp(bindings(
		[4]string{"A", "Some Other Company", "https://other.example/", "NormalRank"},
		[4]string{"AAPL", "Apple Inc.", "https://apple.com/uk/", "NormalRank"},
		[4]string{"AAPL", "Apple Inc.", "https://apple.com/", "NormalRank"},
		[4]string{"AAPL", "Apple Inc.", "https://apple.com/de/", "NormalRank"},
		[4]string{"KO", "The Coca-Cola Company", "https://www.coca-colacompany.com/", "PreferredRank"},
		[4]string{"KO", "The Coca-Cola Company", "https://a.co/", "NormalRank"},
		[4]string{"KO", "The Coca-Cola Company", "ftp://nope/", "NormalRank"},
	)))
	res, err := EnrichWebsites(context.Background(), f, dir, targets, SiteOptions{URL: sitesTest, Now: func() time.Time { return t0 }})
	if err != nil || res.Found != 2 || res.Missing != 2 {
		t.Fatalf("EnrichWebsites = %+v, %v", res, err)
	}
	c, _ := LoadWebsites(dir)
	if c["AAPL"].URL != "https://apple.com/" || c["KO"].URL != "https://www.coca-colacompany.com/" {
		t.Errorf("sites = %+v", c)
	}
	if w, ok := c["A"]; !ok || w.URL != "" {
		t.Errorf("a company of another name must not lend its site: %+v", w)
	}
	if w, ok := c["AWCA"]; !ok || w.URL != "" || w.FetchedAt.IsZero() {
		t.Errorf("a ticker Wikidata lacks is remembered as missing: %+v", w)
	}
	// Young answers are not asked again; missing ones are asked after a month.
	res, _ = EnrichWebsites(context.Background(), f, dir, targets, SiteOptions{URL: sitesTest, Now: func() time.Time { return t0.Add(24 * time.Hour) }})
	if res.Skipped != 4 || len(f.Calls()) != 1 {
		t.Errorf("young answers: %+v, %d calls", res, len(f.Calls()))
	}
	f.Reply(sitesRequestURL(sitesTest, []SiteTarget{{"A", ""}, {"AWCA", ""}}), resp(bindings()))
	res, _ = EnrichWebsites(context.Background(), f, dir, targets, SiteOptions{URL: sitesTest, Now: func() time.Time { return t0.Add(31 * 24 * time.Hour) }})
	if res.Skipped != 2 || res.Missing != 2 {
		t.Errorf("missing answers are asked again after a month: %+v", res)
	}
}

func TestEnrichWebsitesStopsWhenPushedBackAndHonoursMax(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Fail(sitesRequestURL(sitesTest, []SiteTarget{{"A", ""}, {"B", ""}}), &httpx.Error{Class: httpx.Throttled, Status: 429, URL: "x"})
	res, err := EnrichWebsites(context.Background(), f, dir, []SiteTarget{{"C", "C"}, {"B", "B"}, {"A", "A"}}, SiteOptions{URL: sitesTest, Max: 2})
	if err == nil || res.Left != 3 || len(f.Calls()) != 1 {
		t.Errorf("%+v, %v, %d calls: Max keeps the first two by ticker (A, B); the one it left and the two a 429 stopped are all left", res, err, len(f.Calls()))
	}
}
