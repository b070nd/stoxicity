package company

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// The headers, footers and the first rows of each file are copied from the
// live files of 2026-11-05; the other rows are invented to reach the
// financial status letters, a test issue and a share-class symbol.
const nasdaqListed = `Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares
AAAP|Pacer Barings CLO Market Flex ETF|G|N|N|100|Y|N
AACG|ATA Creativity Global - American Depositary Shares, each representing two common shares|S|N|D|100|N|N
AAPL|Apple Inc. - Common Stock|Q|N|N|100|N|N
ZZTEST|Test Security|Q|Y|N|100|N|N
BADCO|Badly Run Co|S|N|K|100|N|N
File Creation Time: 1105202603:02|||||||
`

const otherListed = `ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol
A|Agilent Technologies, Inc. Common Stock|N|A|N|100|N|A
AAA|Alternative Access First Priority CLO Bond ETF|P|AAA|Y|100|N|AAA
AAAA|Amplius Aggressive Asset Allocation ETF|Z|AAAA|Y|100|N|AAAA
BRK.B|Berkshire Hathaway Inc. Class B|N|BRK.B|N|100|N|BRK-B
XYZ|Some Unlisted Code|F|XYZ|N|100|N|XYZ
File Creation Time: 1105202603:02||||||
`

func listingFake(nasdaq, other string) *httpx.Fake {
	f := httpx.NewFake()
	f.Reply(NasdaqListedURL, resp(nasdaq, "ETag", `"n1"`))
	f.Reply(OtherListedURL, resp(other, "ETag", `"o1"`))
	return f
}

func TestRefreshListingStoresAndLoads(t *testing.T) {
	dir := t.TempDir()
	res, err := RefreshListing(context.Background(), listingFake(nasdaqListed, otherListed), dir, ListingOptions{Now: func() time.Time { return t0 }})
	if err != nil || !res.Updated || res.Records != 10 {
		t.Fatalf("RefreshListing = %+v, %v", res, err)
	}
	l, err := LoadListing(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Len() != 10 || !l.FetchedAt.Equal(t0) {
		t.Errorf("Len = %d, FetchedAt = %v", l.Len(), l.FetchedAt)
	}

	cases := []struct {
		ticker string
		want   Security
	}{
		{"AAPL", Security{Symbol: "AAPL", Venue: "Nasdaq", Market: "Global Select"}},
		{"aaap", Security{Symbol: "AAAP", Venue: "Nasdaq", Market: "Global", ETF: true}},
		{"AACG", Security{Symbol: "AACG", Venue: "Nasdaq", Market: "Capital", Status: "deficient"}},
		{"BADCO", Security{Symbol: "BADCO", Venue: "Nasdaq", Market: "Capital", Status: "deficient, delinquent, bankrupt"}},
		{"ZZTEST", Security{Symbol: "ZZTEST", Venue: "Nasdaq", Market: "Global Select", Test: true}},
		{"AAA", Security{Symbol: "AAA", Venue: "NYSE Arca", ETF: true}},
		{"AAAA", Security{Symbol: "AAAA", Venue: "Cboe BZX", ETF: true}},
		{"A", Security{Symbol: "A", Venue: "NYSE"}},
		{"BRK-B", Security{Symbol: "BRK-B", Venue: "NYSE"}}, // the SEC's spelling finds the Nasdaq one
		{"BRK.B", Security{Symbol: "BRK-B", Venue: "NYSE"}},
		{"XYZ", Security{Symbol: "XYZ", Venue: "F"}}, // a code the definitions page does not list is kept as it is
	}
	for _, c := range cases {
		got, ok := l.Get(c.ticker)
		got.Name = "" // the names are checked once below
		if !ok || got != c.want {
			t.Errorf("Get(%q) = %+v, %v; want %+v", c.ticker, got, ok, c.want)
		}
	}
	if s, _ := l.Get("AAPL"); s.Name != "Apple Inc. - Common Stock" {
		t.Errorf("name = %q", s.Name)
	}
	if _, ok := l.Get("NOPE"); ok {
		t.Error("an unknown symbol must not be found")
	}
}

func TestRefreshListingIsYoungThenConditional(t *testing.T) {
	dir := t.TempDir()
	f := listingFake(nasdaqListed, otherListed)
	now := t0
	opts := ListingOptions{Now: func() time.Time { return now }}
	if _, err := RefreshListing(context.Background(), f, dir, opts); err != nil {
		t.Fatal(err)
	}
	res, err := RefreshListing(context.Background(), f, dir, opts)
	if err != nil || !res.Skipped || len(f.Calls()) != 2 {
		t.Fatalf("a young cache should ask nothing: %+v, %v, %d calls", res, err, len(f.Calls()))
	}

	now = t0.Add(8 * 24 * time.Hour)
	f = httpx.NewFake() // a new one, so that the 304 is the first reply
	f.Reply(NasdaqListedURL, httpx.Response{Status: 304, NotModified: true})
	f.Reply(OtherListedURL, httpx.Response{Status: 304, NotModified: true})
	res, err = RefreshListing(context.Background(), f, dir, opts)
	if err != nil || res.Updated || res.Skipped || res.Records != 10 {
		t.Fatalf("after 304: %+v, %v", res, err)
	}
	calls := f.Calls()
	if got := calls[0].ETag; got != `"n1"` {
		t.Errorf("the stored ETag should be sent back, got %q", got)
	}
	l, _ := LoadListing(dir)
	if !l.FetchedAt.Equal(now) {
		t.Errorf("a 304 should renew the time: %v", l.FetchedAt)
	}
}

func TestRefreshListingRefusesBadAnswers(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := RefreshListing(ctx, listingFake(nasdaqListed, otherListed), dir, ListingOptions{}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"html page":     "<html>blocked</html>",
		"header only":   "Symbol|Security Name\n",
		"far too small": "Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\nAAAP|X|G|N|N|100|Y|N\n",
	} {
		_, err := RefreshListing(ctx, listingFake(body, otherListed), dir, ListingOptions{Force: true})
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	l, err := LoadListing(dir)
	if err != nil || l.Len() != 10 {
		t.Errorf("the good cache must survive: %d, %v", l.Len(), err)
	}
}

func TestLoadListingWithoutCacheIsEmptyAndDamagedIsAnError(t *testing.T) {
	dir := t.TempDir()
	l, err := LoadListing(dir)
	if err != nil || l.Len() != 0 {
		t.Fatalf("empty cache: %d, %v", l.Len(), err)
	}
	if err := os.WriteFile(filepath.Join(dir, nasdaqFile), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadListing(dir); err == nil {
		t.Error("a damaged file should be an error")
	}
}

func TestNormalizeSymbol(t *testing.T) {
	for in, want := range map[string]string{"brk.b": "BRK-B", " BF/B ": "BF-B", "AAC.W": "AAC-W", "ABC$D": "ABC-D", "AAPL": "AAPL"} {
		if got := NormalizeSymbol(in); got != want {
			t.Errorf("NormalizeSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- industry ----

// A trimmed record in the shape of data.sec.gov/submissions (Apple's, read on
// 2026-11-05 for the field names and types: cik and sic are strings).
const appleRecord = `{"cik":"0000320193","entityType":"operating","sic":"3571","sicDescription":"Electronic Computers",
"name":"Apple Inc.","tickers":["AAPL"],"exchanges":["Nasdaq"],"website":"","category":"Large accelerated filer",
"fiscalYearEnd":"0926","stateOfIncorporation":"CA",
"formerNames":[{"name":"APPLE INC","from":"2007-01-10T00:00:00.000Z","to":"2019-08-05T00:00:00.000Z"},{"name":"APPLE COMPUTER INC","from":"1994-01-26T00:00:00.000Z","to":"2007-01-09T00:00:00.000Z"}],
"filings":{"recent":{"accessionNumber":["x"]}}}`

const subURL = "https://sub.test/CIK%s.json"

func subAt(cik string) string { return fmt.Sprintf(subURL, cik) }

func TestEnrichStoresIndustryAndSkipsYoungRecords(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Reply(subAt("0000320193"), resp(appleRecord))
	f.Reply(subAt("0000000001"), resp(`{"cik":"0000000001","name":"Tiny Co","sic":"6770","sicDescription":"BLANK CHECKS"}`))
	opts := EnrichOptions{URL: subURL, Now: func() time.Time { return t0 }}
	ctx := context.Background()

	var progress []int
	opts.Progress = func(done, total int) { progress = append(progress, done, total) }
	res, err := Enrich(ctx, f, dir, []string{"320193", "0000320193", "1", "bad", ""}, opts)
	if err != nil || res.Fetched != 2 || len(res.Failed) != 0 {
		t.Fatalf("Enrich = %+v, %v", res, err)
	}
	if fmt.Sprint(progress) != "[1 2 2 2]" {
		t.Errorf("progress = %v", progress)
	}
	c, err := LoadIndustry(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := c["0000320193"]
	if a.SIC != "3571" || a.Description != "Electronic Computers" || a.State != "CA" || a.FiscalYear != "0926" ||
		a.Category != "Large accelerated filer" || len(a.Former) != 2 || a.Former[1] != "APPLE COMPUTER INC" {
		t.Errorf("Apple = %+v", a)
	}

	res, err = Enrich(ctx, f, dir, []string{"320193"}, opts)
	if err != nil || res.Skipped != 1 || res.Fetched != 0 || len(f.Calls()) != 2 {
		t.Errorf("a young record should not be asked again: %+v, %v, %d calls", res, err, len(f.Calls()))
	}
	opts.Now = func() time.Time { return t0.Add(31 * 24 * time.Hour) }
	f.Reply(subAt("0000320193"), resp(appleRecord))
	if res, _ := Enrich(ctx, f, dir, []string{"320193"}, opts); res.Fetched != 1 {
		t.Errorf("an old record should be asked again: %+v", res)
	}
}

func TestEnrichRecordsMissingAndKeepsGoingAfterFailures(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Fail(subAt("0000000001"), &httpx.Error{Class: httpx.Permanent, Status: 404, URL: "x"})
	f.Fail(subAt("0000000002"), &httpx.Error{Class: httpx.Retryable, Status: 500, URL: "x"})
	f.Reply(subAt("0000000003"), resp(`{"cik":"0000000009","name":"Wrong"}`)) // the record is for another CIK
	f.Reply(subAt("0000000004"), resp(`{"cik":"0000000004","name":"Good","sic":"1000"}`))
	res, err := Enrich(context.Background(), f, dir, []string{"1", "2", "3", "4"}, EnrichOptions{URL: subURL, Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Missing != 1 || res.Fetched != 1 || len(res.Failed) != 2 {
		t.Errorf("result = %+v", res)
	}
	c, _ := LoadIndustry(dir)
	if !c["0000000001"].Missing || c["0000000004"].SIC != "1000" || len(c) != 2 {
		t.Errorf("cache = %+v", c)
	}
}

func TestEnrichStopsWhenTheSECPushesBackAndKeepsWhatItHas(t *testing.T) {
	dir := t.TempDir()
	f := httpx.NewFake()
	f.Reply(subAt("0000000001"), resp(`{"cik":"0000000001","name":"One"}`))
	f.Fail(subAt("0000000002"), &httpx.Error{Class: httpx.Throttled, Status: 429, URL: "x"})
	res, err := Enrich(context.Background(), f, dir, []string{"1", "2", "3"}, EnrichOptions{URL: subURL})
	if err == nil || res.Fetched != 1 || res.Left != 2 {
		t.Fatalf("Enrich = %+v, %v", res, err)
	}
	if len(f.Calls()) != 2 {
		t.Errorf("it must not go on after a 429: %d calls", len(f.Calls()))
	}
	if c, _ := LoadIndustry(dir); len(c) != 1 {
		t.Errorf("the record fetched before the stop must be saved: %v", c)
	}
}

func TestEnrichMaxLimitsRequests(t *testing.T) {
	f := httpx.NewFake()
	for _, c := range []string{"0000000001", "0000000002", "0000000003"} {
		f.Reply(subAt(c), resp(fmt.Sprintf(`{"cik":%q,"name":"N"}`, c)))
	}
	res, err := Enrich(context.Background(), f, t.TempDir(), []string{"3", "1", "2"}, EnrichOptions{URL: subURL, Max: 2})
	if err != nil || res.Fetched != 2 || res.Left != 1 || len(f.Calls()) != 2 {
		t.Errorf("%+v, %v, %d calls", res, err, len(f.Calls()))
	}
}

func TestEnrichCancelledContextKeepsProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := httpx.NewFake()
	f.Reply(subAt("0000000001"), resp(`{"cik":"0000000001","name":"One"}`))
	dir := t.TempDir()
	opts := EnrichOptions{URL: subURL, Progress: func(done, _ int) { cancel() }}
	_, err := Enrich(ctx, f, dir, []string{"1", "2"}, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if c, _ := LoadIndustry(dir); len(c) != 1 {
		t.Errorf("cache = %v", c)
	}
}

func TestLoadIndustryDamagedIsAnError(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, industryFile), []byte("{"), 0o644)
	if _, err := LoadIndustry(dir); err == nil {
		t.Error("want an error")
	}
}
