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

const acmeRecord = `{"cik":"0001000001","entityType":"operating","sic":"7372","sicDescription":"Services-Prepackaged Software","name":"Acme Corp","fiscalYearEnd":"1231","stateOfIncorporation":"DE","category":"Large accelerated filer","exchanges":["Nasdaq"],"phone":"555-0100","ein":"123456789","addresses":{"business":{"city":"SPRINGFIELD","stateOrCountryDescription":"IL"}},"formerNames":[{"name":"ACME HOLDINGS"}],"filings":{"recent":{"accessionNumber":["0001000001-26-000009","0001000001-26-000007"],"form":["8-K","10-Q"],"filingDate":["2026-11-01","2026-10-25"],"reportDate":["","2026-09-30"],"primaryDocument":["k.htm","q.htm"]}}}`

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
	// Polygon's grouped daily bars: the same two stocks on every day asked for, one
	// of them with a candle the program must refuse; and its list of splits (none).
	// The SEC's history of Acme's facts and the cover page of its quarterly report.
	mux.HandleFunc("/fund/companyfacts/CIK0001000001.json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, acmeFacts) })
	mux.HandleFunc("/fund/archives/1000001/000100000126000007/q.htm", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, acmeCover) })
	// The SEC's index of the filings of Monday 2 November: two wanted, one not.
	mux.HandleFunc("/Archives/edgar/daily-index/2026/QTR4/master.20261102.idx", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, dailyIndex) })
	mux.HandleFunc("/ecb/daily.xml", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, ecbDaily) })
	mux.HandleFunc("/polygon/splits", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"results":[]}`) })
	mux.HandleFunc("/polygon/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[{"T":"ACME","t":1700000000000,"o":10,"h":12,"l":9.5,"c":11.5,"v":1000},{"T":"GLOB","o":20,"h":19,"l":21,"c":20,"v":5},{"T":"NOTLISTED","o":1,"h":2,"l":1,"c":2,"v":1}]}`)
	})
	// Wikidata's answer: Acme has a site; the other company of the ticker does not match.
	var srv *httptest.Server
	mux.HandleFunc("/sparql", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"results":{"bindings":[{"t":{"value":"ACME"},"label":{"value":"Acme Corporation"},"site":{"value":%q},"rank":{"value":"http://wikiba.se/ontology#NormalRank"}}]}}`, srv.URL+"/acme/")
	})
	srv = httptest.NewTLSServer(mux)
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
	write("config.yaml", "since: 2026-11-01\ndata_dir: ./data\nwatchlist: [ACME]\ncompany:\n  history: off\n  url: "+srv.URL+"/company_tickers.json\n  nasdaq_url: "+srv.URL+"/nasdaqlisted.txt\n  other_url: "+srv.URL+"/otherlisted.txt\n  submissions_url: "+srv.URL+"/sub/CIK%s.json\n  sites_url: "+srv.URL+"/sparql\n")
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

// ecbDaily is an invented document in the shape of the ECB's reference rates.
const ecbDaily = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
<gesmes:subject>Reference rates</gesmes:subject>
<Cube><Cube time="2026-11-06"><Cube currency="USD" rate="1.0850"/><Cube currency="GBP" rate="0.8412"/><Cube currency="IDR" rate="17000.5"/></Cube>
<Cube time="2026-11-05"><Cube currency="USD" rate="1.0900"/></Cube></Cube>
</gesmes:Envelope>`

const acmeFacts = `{"cik":1000001,"facts":{"us-gaap":{"Revenues":{"units":{"USD":[
 {"start":"2024-01-01","end":"2024-12-31","val":100,"accn":"0001000001-25-000001","form":"10-K","filed":"2025-02-20"},
 {"start":"2024-01-01","end":"2024-12-31","val":110,"accn":"0001000001-25-000099","form":"10-K/A","filed":"2025-06-01"}]}}}}}`

const acmeCover = `<?xml version="1.0"?><html xmlns:ix="http://www.xbrl.org/2013/inlineXBRL" xmlns:xbrli="http://www.xbrl.org/2003/instance"><body>
<xbrli:context id="c"><xbrli:entity></xbrli:entity><xbrli:period><xbrli:instant>2026-10-20</xbrli:instant></xbrli:period></xbrli:context>
<ix:nonFraction name="dei:EntityCommonStockSharesOutstanding" contextRef="c" unitRef="shares" scale="0">2,000,000</ix:nonFraction></body></html>`

const dailyIndex = `Description:           Master Index of EDGAR Dissemination Feed
Last Data Received:    November 2, 2026
Comments:              webmaster@sec.gov
Anonymous FTP:         ftp://ftp.sec.gov/edgar/

CIK|Company Name|Form Type|Date Filed|Filename
--------------------------------------------------------------------------------
1000001|ACME CORP|8-K|20261102|edgar/data/1000001/0001000001-26-000011.txt
1000001|ACME CORP|4|20261102|edgar/data/1000001/0001000001-26-000012.txt
1000001|ACME CORP|424B2|20261102|edgar/data/1000001/0001000001-26-000013.txt
`
