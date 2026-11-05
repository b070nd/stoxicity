package universe

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/model"
)

const nasdaq = `Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares
AAPL|Apple Inc. - Common Stock|Q|N|N|100|N|N
NVDA|NVIDIA Corporation - Common Stock|Q|N|N|100|N|N
QQQ|Invesco QQQ Trust|G|N|N|100|Y|N
BADCO|Badly Run Co|S|N|D|100|N|N
`

const other = `ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol
KO|The Coca-Cola Company Common Stock|N|KO|N|100|N|KO
BRK.B|Berkshire Hathaway Inc. Class B|N|BRK.B|N|100|N|BRK-B
SPY|SPDR S&P 500 ETF Trust|P|SPY|Y|100|N|SPY
`

func listing(t *testing.T) company.Listing {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"nasdaqlisted.txt": nasdaq, "otherlisted.txt": other} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l, err := company.LoadListing(dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func master() company.Master {
	return company.Master{Entities: []model.Entity{
		{Ticker: "AAPL", Name: "Apple Inc.", CIK: "0000320193", Exchange: "Nasdaq"},
		{Ticker: "NVDA", Name: "NVIDIA CORP", CIK: "0001045810", Exchange: "Nasdaq"},
		{Ticker: "KO", Name: "COCA COLA CO", CIK: "0000021344", Exchange: "NYSE"},
		{Ticker: "BRK-B", Name: "BERKSHIRE HATHAWAY INC", CIK: "0001067983", Exchange: "NYSE"},
		{Ticker: "BRK-A", Name: "BERKSHIRE HATHAWAY INC", CIK: "0001067983", Exchange: "NYSE"},
		{Ticker: "QQQ", Name: "Invesco QQQ Trust, Series 1", CIK: "0001067839", Exchange: "Nasdaq"},
		{Ticker: "BADCO", Name: "Badly Run Co", CIK: "0009000001", Exchange: "OTC"},
		{Ticker: "ACME", Name: "ACME SOFTWARE CORP /DE/", CIK: "0009000002", Exchange: "Nasdaq"},
	}}
}

func industry() company.IndustryCache {
	return company.IndustryCache{
		"0000320193": {CIK: "0000320193", SIC: "3571", Description: "Electronic Computers", State: "CA", FiscalYear: "0926",
			Former: []string{"APPLE COMPUTER INC"}, Category: "Large accelerated filer"},
		"0001045810": {CIK: "0001045810", SIC: "3674", Description: "Semiconductors & Related Devices", State: "DE"},
		"0009000002": {CIK: "0009000002", SIC: "7372", Description: "Services-Prepackaged Software", State: "DE"},
		"0000021344": {CIK: "0000021344", Missing: true},
	}
}

func layer() Layer {
	return Layer{
		Lists: []ListDef{{ID: "compounders", Name: "Compounders", Tickers: []string{"ko", "BRK-B", "NESN"}}, {ID: "ai", Tickers: []string{"NVDA"}}},
		Tags:  map[string][]string{"KO": {"moat", "dividend"}, "NVDA": {"moat"}},
		Extra: []Extra{{Ticker: "NESN", Name: "Nestlé", Exchange: "SIX", ISIN: "CH0038863350", Aliases: []string{"Nestle"}, Sector: "staples"}},
	}
}

func build(t *testing.T) *Universe {
	t.Helper()
	return New(Inputs{
		Master: master(), Listing: listing(t), Industry: industry(), Layer: layer(),
		Sectors: map[string]string{"NVDA": "technology", "AAPL": "technology"},
		Known:   []string{"msft", "KO"},
	})
}

func tickers(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Profile.Ticker
	}
	return out
}

func find(t *testing.T, u *Universe, expr string, f Facts) []string {
	t.Helper()
	q, err := ParseQuery(expr)
	if err != nil {
		t.Fatalf("%q: %v", expr, err)
	}
	return tickers(u.Find(q, f, 0))
}

func TestNewJoinsTheSources(t *testing.T) {
	u := build(t)
	if u.Len() != 10 { // 8 SEC + NESN (extra) + MSFT (known only)
		t.Fatalf("Len = %d: %v", u.Len(), tickers(u.Find(Query{}, Facts{}, 0)))
	}
	a, ok := u.Get("AAPL")
	if !ok {
		t.Fatal("no AAPL")
	}
	if a.Name != "Apple Inc." || a.CIK != "0000320193" || a.Exchange != "Nasdaq" || a.Venue != "Nasdaq" || a.Market != "Global Select" ||
		a.Kind != KindStock || a.SIC != "3571" || a.Industry != "Electronic Computers" || a.State != "CA" || a.Sector != "technology" ||
		a.Source != SourceSEC || !slices.Contains(a.Aliases, "APPLE COMPUTER INC") {
		t.Errorf("AAPL = %+v", a)
	}
	n, _ := u.Get("NVDA")
	if n.Name != "Nvidia Corp" || !slices.Contains(n.Aliases, "NVIDIA CORP") || !slices.Equal(n.Lists, []string{"ai"}) || !slices.Equal(n.Tags, []string{"moat"}) {
		t.Errorf("NVDA = %+v", n)
	}
	k, _ := u.Get("ko")
	if !slices.Equal(k.Lists, []string{"compounders"}) || !slices.Equal(k.Tags, []string{"dividend", "moat"}) || k.Venue != "NYSE" || k.Industry != "" {
		t.Errorf("KO = %+v (a missing industry record gives no industry)", k)
	}
	if q, _ := u.Get("QQQ"); q.Kind != KindETF {
		t.Errorf("QQQ kind = %q", q.Kind)
	}
	if b, _ := u.Get("BADCO"); b.Status != "deficient" || b.Exchange != "OTC" || b.Venue != "Nasdaq" || b.Kind != KindStock {
		t.Errorf("BADCO = %+v", b)
	}
	if b, _ := u.Get("BRK-A"); b.Kind != "" || b.Venue != "" {
		t.Errorf("BRK-A = %+v (not in the Nasdaq files: no kind, no venue)", b)
	}
	if x, _ := u.Get("NESN"); x.Source != SourceExtra || x.ISIN != "CH0038863350" || x.Sector != "staples" || x.Name != "Nestlé" || !slices.Equal(x.Lists, []string{"compounders"}) {
		t.Errorf("NESN = %+v", x)
	}
	if m, _ := u.Get("MSFT"); m.Source != SourceUser || m.Name != "" {
		t.Errorf("MSFT = %+v", m)
	}
}

func TestGetAcceptsBothSpellingsOfAShareClass(t *testing.T) {
	u := build(t)
	for _, in := range []string{"BRK-B", "brk.b", "BRK/B"} {
		p, ok := u.Get(in)
		if !ok || p.Ticker != "BRK-B" {
			t.Errorf("Get(%q) = %+v, %v", in, p, ok)
		}
	}
	p, _ := u.Get("BRK-B")
	if !slices.Equal(p.Siblings, []string{"BRK-A"}) {
		t.Errorf("siblings = %v", p.Siblings)
	}
	if _, ok := u.Get("NOPE"); ok {
		t.Error("unknown ticker")
	}
	var nilU *Universe
	if _, ok := nilU.Get("AAPL"); ok || nilU.Len() != 0 || nilU.All() != nil {
		t.Error("a nil Universe is empty")
	}
}

func TestFindRanksTickerThenNameStartThenWordThenAlias(t *testing.T) {
	u := build(t)
	cases := map[string][]string{
		"apple":          {"AAPL"},
		"app":            {"AAPL"},
		"aapl":           {"AAPL"},
		"nvidia":         {"NVDA"},
		"coca cola":      {"KO"},
		`"coca cola"`:    {"KO"},
		"berkshire":      {"BRK-A", "BRK-B"},
		"hathaway inc":   {"BRK-A", "BRK-B"},
		"apple computer": {"AAPL"}, // a former name
		"nestle":         {"NESN"}, // an alias without the accent
		"software":       {"ACME"}, // name word and industry
		"semiconductors": {"NVDA"}, // industry only
		"brk.b":          {"BRK-B"},
		"qq":             {"QQQ"},
		"zzzz":           nil,
		"invesco -etf":   {"QQQ"}, // -etf is a text term here, QQQ's name has no "etf"
	}
	for expr, want := range cases {
		if got := find(t, u, expr, Facts{}); !slices.Equal(got, want) {
			t.Errorf("%q = %v, want %v", expr, got, want)
		}
	}
	// A ticker beats a name: "ko" is KO's ticker and starts no name.
	if got := find(t, u, "ko", Facts{}); got[0] != "KO" {
		t.Errorf("ko = %v", got)
	}
	// An exact ticker outranks a longer ticker with the same start.
	got := find(t, u, "brk-a", Facts{})
	if len(got) == 0 || got[0] != "BRK-A" {
		t.Errorf("brk-a = %v", got)
	}
}

func TestFindTieBreaksOnNewsThenTicker(t *testing.T) {
	u := build(t)
	f := Facts{News: map[string]int{"NVDA": 5, "KO": 9, "AAPL": 5}}
	got := find(t, u, "tag:moat OR ticker:AAPL", f)
	if !slices.Equal(got, []string{"KO", "AAPL", "NVDA"}) {
		t.Errorf("got %v: news first, then ticker", got)
	}
	got = find(t, u, "berkshire", Facts{News: map[string]int{"BRK-B": 1}})
	if !slices.Equal(got, []string{"BRK-B", "BRK-A"}) {
		t.Errorf("equal text scores fall back on news: %v", got)
	}
}

func TestOperators(t *testing.T) {
	u := build(t)
	f := Facts{News: map[string]int{"NVDA": 4, "AAPL": 1}, Watched: map[string]bool{"KO": true, "AAPL": true}}
	cases := map[string][]string{
		"ticker:ko":                      {"KO"},
		"ticker:BRK*":                    {"BRK-A", "BRK-B"},
		"ticker:brk.b":                   {"BRK-B"},
		"name:nvidia":                    {"NVDA"},
		"company:coca":                   {"KO"},
		`name:"berkshire hathaway"`:      {"BRK-A", "BRK-B"},
		"cik:320193":                     {"AAPL"},
		"cik:0000320193":                 {"AAPL"},
		"exchange:nyse":                  {"BRK-A", "BRK-B", "KO"},
		"exchange:SIX":                   {"NESN"},
		"exchange:otc":                   {"BADCO"},
		"kind:etf":                       {"QQQ"},
		"kind:stock -exchange:otc":       {"AAPL", "BRK-B", "KO", "NVDA"},
		"industry:software":              {"ACME"},
		"industry:semi":                  {"NVDA"},
		"sic:7372":                       {"ACME"},
		"sic:3*":                         {"AAPL", "NVDA"},
		"state:de":                       {"ACME", "NVDA"},
		"sector:technology":              {"AAPL", "NVDA"},
		`market:"global select"`:         {"AAPL", "NVDA"},
		"status:deficient":               {"BADCO"},
		"status:any":                     {"BADCO"},
		"list:compounders":               {"BRK-B", "KO", "NESN"},
		"list:compounders -tag:dividend": {"BRK-B", "NESN"},
		"tag:moat":                       {"KO", "NVDA"},
		"tag:moat list:ai":               {"NVDA"},
		"watched:yes":                    {"AAPL", "KO"},
		"watched:no tag:moat":            {"NVDA"},
		"news:yes":                       {"NVDA", "AAPL"},
		"news:>=2":                       {"NVDA"},
		"news:<2 ticker:A*":              {"AAPL", "ACME"},
		"news:0 ticker:a*":               {"ACME"},
		"kind:etf OR kind:test":          {"QQQ"},
		"apple | nvidia":                 {"AAPL", "NVDA"},
		"tag:moat -nvidia":               {"KO"},
	}
	for expr, want := range cases {
		got := find(t, u, expr, f)
		slices.Sort(got)
		if want = slices.Clone(want); !slices.Equal(got, want) && !sameSet(got, want) {
			t.Errorf("%q = %v, want %v", expr, got, want)
		}
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func TestParseQueryErrors(t *testing.T) {
	for expr, want := range map[string]string{
		"nope:x":                 "unknown operator",
		"ticker:":                "needs a value",
		"kind:bond":              "unknown kind",
		"watched:maybe":          "yes or no",
		"news:lots":              "news takes",
		`"unterminated`:          "unterminated",
		"| apple":                "OR needs a term before",
		"apple |":                "OR needs a term after",
		"apple OR":               "OR needs a term after",
		"-":                      "letters or digits",
		"***":                    "letters or digits",
		strings.Repeat("a", 501): "longer than",
		strings.Repeat("a ", 41): "more than",
	} {
		_, err := ParseQuery(expr)
		var qe *QueryError
		if !errors.As(err, &qe) || !strings.Contains(qe.Msg, want) {
			t.Errorf("%q: err = %v, want %q", expr[:min(len(expr), 20)], err, want)
		}
	}
	_, err := ParseQuery("apple nope:x")
	var qe *QueryError
	if errors.As(err, &qe) && qe.Pos != len("apple ") {
		t.Errorf("Pos = %d", qe.Pos)
	}
}

func TestEmptyQueryListsEveryoneByNewsThenTicker(t *testing.T) {
	u := build(t)
	q, err := ParseQuery("   ")
	if err != nil || !q.IsEmpty() {
		t.Fatal(err)
	}
	hits := u.Find(q, Facts{News: map[string]int{"NVDA": 2}}, 3)
	if got := tickers(hits); !slices.Equal(got, []string{"NVDA", "AAPL", "ACME"}) {
		t.Errorf("got %v", got)
	}
}

func TestFacetsCountTheResult(t *testing.T) {
	u := build(t)
	q, _ := ParseQuery("")
	f := FacetsOf(u.Find(q, Facts{}, 0), 3)
	if f.Exchange[0] != (Count{"Nasdaq", 4}) {
		t.Errorf("exchange = %v", f.Exchange)
	}
	if len(f.Exchange) != 3 {
		t.Errorf("n caps the lists: %v", f.Exchange)
	}
	if f.Tag[0].Name != "moat" || f.Tag[0].Count != 2 {
		t.Errorf("tags = %v", f.Tag)
	}
	if f.List[0] != (Count{"compounders", 3}) {
		t.Errorf("lists = %v", f.List)
	}
	for _, c := range f.Kind {
		if c.Name == "" {
			t.Error("an empty kind is not a facet value")
		}
	}
}

func TestListsAndTags(t *testing.T) {
	u := build(t)
	l, ok := u.List("Compounders")
	if !ok || l.Name != "Compounders" || !slices.Equal(l.Tickers, []string{"BRK-B", "KO", "NESN"}) {
		t.Errorf("list = %+v, %v", l, ok)
	}
	if ai, _ := u.List("ai"); ai.Name != "ai" {
		t.Errorf("a list without a name shows its ID: %+v", ai)
	}
	if _, ok := u.List("nope"); ok {
		t.Error("no such list")
	}
	if got := u.Tags(); len(got) != 2 || got[0] != (Count{"moat", 2}) || got[1] != (Count{"dividend", 1}) {
		t.Errorf("tags = %v", got)
	}
}

func TestFilterEnvTurnsCompaniesIntoTickersOfArticles(t *testing.T) {
	u := build(t)
	env := u.FilterEnv()
	art := func(tickers ...string) model.Article {
		return model.Article{Title: "Results", Tickers: tickers, Published: time.Now()}
	}
	cases := []struct {
		expr string
		art  model.Article
		want bool
	}{
		{"company:apple", art("AAPL"), true},
		{"company:apple", art("NVDA"), false},
		{"company:berkshire", art("BRK-A"), true},
		{"list:compounders", art("KO"), true},
		{"list:compounders", art("NVDA"), false},
		{"-list:compounders results", art("NVDA"), true},
		{"tag:moat", art("X", "nvda"), true},
		{"tag:unknown", art("KO"), false},
		{"exchange:nyse", art("KO"), true},
		{"industry:software", art("ACME"), true},
		{"sic:73*", art("ACME"), true},
		{"state:de", art("NVDA"), true},
		{"cik:1045810", art("NVDA"), true},
		{"list:ai OR company:apple", art("AAPL"), true},
		{"ticker:KO list:compounders", art("KO"), true},
	}
	for _, c := range cases {
		q, err := filter.ParseWith(c.expr, env)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if got := q.Match(c.art); got != c.want {
			t.Errorf("%q on %v = %v, want %v", c.expr, c.art.Tickers, got, c.want)
		}
	}
	_, err := filter.ParseWith("list:nope", env)
	if err == nil || !strings.Contains(err.Error(), "ai, compounders") {
		t.Errorf("an unknown list should name the lists: %v", err)
	}
	if _, err := filter.ParseWith("list:x", (*Universe)(nil).FilterEnv()); err == nil || !strings.Contains(err.Error(), "no lists yet") {
		t.Errorf("err = %v", err)
	}
}
