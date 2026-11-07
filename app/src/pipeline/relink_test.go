package pipeline

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cikFixture is a pipeline whose company list knows CIKs, with the entities
// of a filing's two worst enemies: a fund whose ticker is the word CIK and a
// company named like a word in the filer's name.
func cikFixture(t *testing.T) (*Pipeline, *store.DB) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	master := company.Master{Entities: []model.Entity{
		{Ticker: "AACI", Name: "Armada Acquisition Corp. III", CIK: "0002092897", Exchange: "Nasdaq"},
		{Ticker: "AACIW", Name: "Armada Acquisition Corp. III", CIK: "0002092897", Exchange: "Nasdaq"},
		{Ticker: "CIK", Name: "UBS Asset Management Income Fund, Inc.", CIK: "0000774608", Exchange: "NYSE"},
		{Ticker: "GPI", Name: "Group 1 Automotive Inc", CIK: "0001031203", Exchange: "NYSE"},
		{Ticker: "PRCH", Name: "Porch Group, Inc.", CIK: "0001784535", Exchange: "Nasdaq"},
	}}
	l := lens.New(config.Lens{Threshold: 0.5}, lens.Options{})
	return New(Config{Store: db, Lens: l, Entities: company.NewResolver(master, nil), Now: func() time.Time { return testNow }}), db
}

func filing(id, title, cik string, tickers ...string) model.Article {
	return model.Article{
		ID: id, URL: "https://www.sec.gov/Archives/" + id, Title: title, Published: testNow, Fetched: testNow,
		Summary: "Form 10-Q filed by " + title + " (CIK " + cik + "), accession " + id,
		Form:    "10-Q", CIK: cik, Tickers: tickers,
	}
}

func TestAFilingGetsTheTickersOfItsCIKAndNothingElse(t *testing.T) {
	p, db := cikFixture(t)
	ctx := context.Background()
	src := model.Source{ID: "sec-edgar", Domain: "sec.gov", Licence: model.LicenceRedistributable}
	_, err := p.Ingest(ctx, src, []model.Article{
		// The summary holds the word CIK and the title names another company.
		filing("0001-26-1", "10-Q - Armada Acquisition Corp. III, Group 1 Automotive", "0002092897", "CIK", "GPI"),
		// A filer the SEC list does not know gets no ticker, and none from its text.
		filing("0001-26-2", "8-K - Porch Group, Inc. (NASDAQ: PRCH)", "0009999999"),
		// News is not a filing: its text still decides.
		{ID: "n1", URL: "https://example.com/n1", Title: "Porch Group (NASDAQ: PRCH) raises guidance", Published: testNow, Fetched: testNow},
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][]string{"0001-26-1": {"AACI", "AACIW"}, "0001-26-2": nil, "n1": {"PRCH"}} {
		a, err := db.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(a.Tickers, want) {
			t.Errorf("%s: tickers %v, want %v", id, a.Tickers, want)
		}
	}
	if a, _ := db.Get(ctx, "0001-26-1"); a.CIK != "0002092897" {
		t.Errorf("the CIK was not stored: %q", a.CIK)
	}
}

func TestRelinkRepairsStoredFilingsAndLeavesTheRestAlone(t *testing.T) {
	p, db := cikFixture(t)
	ctx := context.Background()
	bad := filing("0001-26-1", "10-Q - Porch Group, Inc.", "0001784535", "CIK", "GPI", "PRCH")
	good := filing("0001-26-2", "10-Q - Armada Acquisition Corp. III", "0002092897", "AACI", "AACIW")
	unknown := filing("0001-26-3", "10-Q - Some Fund", "0005555555", "CIK")
	news := model.Article{ID: "n1", URL: "https://example.com/n1", Title: "Wild words", Published: testNow, Fetched: testNow, Tickers: []string{"CIK"}}
	if err := db.Save(ctx, []model.Article{bad, good, unknown, news}); err != nil {
		t.Fatal(err)
	}
	res, err := p.Relink(ctx, testNow.AddDate(0, 0, -1), testNow.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if res.Filings != 3 || res.Changed != 2 || res.Cleared != 1 {
		t.Errorf("result %+v, want 3 filings, 2 changed, 1 left without a ticker", res)
	}
	for id, want := range map[string][]string{"0001-26-1": {"PRCH"}, "0001-26-2": {"AACI", "AACIW"}, "0001-26-3": nil, "n1": {"CIK"}} {
		a, _ := db.Get(ctx, id)
		if !slices.Equal(a.Tickers, want) {
			t.Errorf("%s: tickers %v, want %v", id, a.Tickers, want)
		}
	}
	// A second run finds nothing to do.
	if res, _ = p.Relink(ctx, testNow.AddDate(0, 0, -1), testNow.AddDate(0, 0, 1)); res.Changed != 0 {
		t.Errorf("second run changed %d", res.Changed)
	}
}
