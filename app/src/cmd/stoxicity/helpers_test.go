package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// testNow is the clock of the tests: 11:00 in Berlin on 2026-11-03.
var testNow = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

// Synthetic fixture, not verified against live data: an Atom feed with two
// entries, written from the documented format.
const atomFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Synthetic news</title>
  <id>urn:uuid:5f1b6a40-0000-4000-8000-000000000001</id>
  <updated>2026-11-03T09:30:00Z</updated>
  <entry>
    <title>Acme Corp announces record earnings and a new share buyback</title>
    <link href="https://news.example.com/acme-earnings"/>
    <id>urn:uuid:5f1b6a40-0000-4000-8000-000000000011</id>
    <published>2026-11-03T08:00:00Z</published>
    <updated>2026-11-03T08:00:00Z</updated>
    <summary>Acme Corp (NASDAQ: ACME) reported record free cash flow and approved a buyback.</summary>
  </entry>
  <entry>
    <title>Federal Reserve leaves interest rates unchanged this week</title>
    <link href="https://news.example.com/fed-rates"/>
    <id>urn:uuid:5f1b6a40-0000-4000-8000-000000000012</id>
    <published>2026-11-03T09:00:00Z</published>
    <updated>2026-11-03T09:00:00Z</updated>
    <summary>The committee kept the federal funds rate where it was.</summary>
  </entry>
</feed>`

// Synthetic fixture in the shape of the SEC's company_tickers.json.
const companyList = `{
 "0":{"cik_str":1000001,"ticker":"ACME","title":"ACME CORP"},
 "1":{"cik_str":1000002,"ticker":"GLOBB","title":"Globex Holdings Inc"},
 "2":{"cik_str":1000002,"ticker":"GLOB","title":"Globex Holdings Inc"}
}`

// Synthetic fixtures in the shapes of the Nasdaq Trader files (header, rows,
// footer) and of one SEC submissions record.
const nasdaqFile = `Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares
ACME|Acme Corp - Common Stock|Q|N|N|100|N|N
ETFX|Example Growth ETF|G|N|N|100|Y|N
File Creation Time: 1105202603:02|||||||
`

const otherFile = `ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol
GLOB|Globex Holdings Inc Common Stock|N|GLOB|N|100|N|GLOB
File Creation Time: 1105202603:02||||||
`

const acmeRecord = `{"cik":"0001000001","entityType":"operating","sic":"7372","sicDescription":"Services-Prepackaged Software","name":"Acme Corp","fiscalYearEnd":"1231","stateOfIncorporation":"DE","category":"Large accelerated filer","formerNames":[{"name":"ACME HOLDINGS"}]}`

// testEnv is a configuration folder in a temporary directory and a feed server.
type testEnv struct {
	dir    string
	client httpx.Client
	server *httptest.Server
}

// newTestEnv writes a configuration with one enabled RSS source "news-feed"
// that reads the synthetic feed, one disabled source "broken" whose URL gives a
// 404, and the default tenant with one watch. It clears the STOXICITY_*
// variables the program reads and sets a contact address.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	for _, name := range []string{"STOXICITY_TIMEZONE", "STOXICITY_DATA_DIR", "STOXICITY_TENANT", "STOXICITY_LOG_LEVEL", "STOXICITY_LAYA_URL"} {
		t.Setenv(name, "")
	}
	t.Setenv("STOXICITY_CONTACT", "tester@example.com")

	mux := http.NewServeMux()
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		fmt.Fprint(w, atomFeed)
	})
	mux.HandleFunc("/company_tickers.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"company-1"`)
		fmt.Fprint(w, companyList)
	})
	mux.HandleFunc("/nasdaqlisted.txt", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, nasdaqFile) })
	mux.HandleFunc("/otherlisted.txt", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, otherFile) })
	mux.HandleFunc("/sub/CIK0001000001.json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, acmeRecord) })
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	client, err := httpx.New(httpx.Config{
		UserAgent:    "stoxicity-test/0 (tester@example.com)",
		Transport:    srv.Client().Transport,
		AllowPrivate: true,
		MaxAttempts:  1,
	})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}

	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.yaml", "since: 2026-11-01\ndata_dir: ./data\nwatchlist: [ACME]\ncompany:\n  url: "+srv.URL+"/company_tickers.json\n  nasdaq_url: "+srv.URL+"/nasdaqlisted.txt\n  other_url: "+srv.URL+"/otherlisted.txt\n  submissions_url: "+srv.URL+"/sub/CIK%s.json\n")
	write("sources.yaml", fmt.Sprintf(`sources:
  - id: news-feed
    name: Synthetic news
    domain: news.example.com
    kind: rss
    url: %[1]s/feed.xml
    tier: 2
    licence: redistributable
    terms: https://example.com/terms
    enabled: true
  - id: broken
    name: Missing feed
    domain: news.example.com
    kind: rss
    url: %[1]s/missing.xml
    tier: 2
    licence: redistributable
    terms: https://example.com/terms
    enabled: false
`, srv.URL))
	write("tenants/default/tenant.yaml", "id: default\nname: Default\ndefault_watch: all-relevant\n")
	write("tenants/default/watches/all-relevant.yaml", "id: all-relevant\nname: Everything relevant\n")
	return &testEnv{dir: dir, client: client, server: srv}
}

// result is what one run of the program produced.
type result struct {
	code        int
	out, errOut string
}

// run runs the program in the test environment with args after the global
// flags that point it at the environment.
func (e *testEnv) run(t *testing.T, args ...string) result {
	t.Helper()
	return runWith(t, runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return testNow }},
		append([]string{"-config-dir", e.dir}, args...)...)
}

func runWith(t *testing.T, opts runOptions, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, &out, &errOut, opts)
	return result{code: code, out: out.String(), errOut: errOut.String()}
}
