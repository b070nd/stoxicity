package instrument

import (
	"bytes"
	"strings"
	"testing"
)

const day = "2026-11-08"

func registry(t *testing.T) *Registry {
	t.Helper()
	r, ps, err := New(ExampleSet())
	if err != nil {
		t.Fatalf("%v\n%v", err, ps)
	}
	return r
}

func TestIdentifierChecks(t *testing.T) {
	for _, ok := range []string{"US0378331005", "DE0007164600", "GB0002634946"} {
		if err := ValidISIN(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "US037833100", "US0378331006", "us0378331005", "1S0378331005", "US037833100!", "US0378331005X"} {
		if err := ValidISIN(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if d, err := ISINCheckDigit("US037833100"); err != nil || d != '5' {
		t.Errorf("check digit %c %v", d, err)
	}
	for _, ok := range []string{"XNAS", "XETR", "X123"} {
		if err := ValidMIC(ok); err != nil {
			t.Error(err)
		}
	}
	for _, bad := range []string{"", "xnas", "XNA", "XNASS", "XN-S"} {
		if err := ValidMIC(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	// A known LEI test value (ISO 17442 example form) is not available offline, so
	// build one and break it.
	body := "529900T8BM49AURSDO"
	lei := body + checkLEI(t, body)
	if err := ValidLEI(lei); err != nil {
		t.Errorf("%s: %v", lei, err)
	}
	if err := ValidLEI(body + "00"); err == nil {
		t.Error("a wrong LEI check")
	}
}

// checkLEI computes the two check digits of an LEI body for the test.
func checkLEI(t *testing.T, body string) string {
	t.Helper()
	d, err := expand(body + "00")
	if err != nil {
		t.Fatal(err)
	}
	rem := 0
	for _, r := range d {
		rem = (rem*10 + int(r-'0')) % 97
	}
	c := 98 - rem
	return string([]byte{byte('0' + c/10), byte('0' + c%10)})
}

func TestTheExampleSetIsValid(t *testing.T) {
	ps := Validate(ExampleSet())
	if errs := Errors(ps); len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, p := range ps {
		t.Logf("%s", p)
	}
}

func TestUSStockResolvesToOneListing(t *testing.T) {
	r := registry(t)
	res := r.ResolveListing(Query{Ticker: "aapl", Day: day})
	if res.Status != Resolved || res.Listing.MIC != "XNAS" || res.Listing.Currency != "USD" || res.Security.ISIN != "US0378331005" || res.Issuer.CIK != "0000320193" {
		t.Fatalf("%+v", res)
	}
	if res2 := r.ResolveListing(Query{ISIN: "US0378331005", Day: day}); res2.Status != Resolved || res2.Listing.ID != res.Listing.ID {
		t.Errorf("by ISIN: %+v", res2)
	}
}

func TestEuroStockAndVenue(t *testing.T) {
	r := registry(t)
	res := r.ResolveListing(Query{Ticker: "SAP", MIC: "XETR", Day: day})
	if res.Status != Resolved || res.Listing.Currency != "EUR" || res.Listing.QuoteUnit != "EUR" {
		t.Fatalf("%+v", res)
	}
	// SAP is not listed in Paris in this set.
	if res := r.ResolveListing(Query{Ticker: "SAP", MIC: "XPAR", Day: day}); res.Status != NotFound {
		t.Errorf("%+v", res)
	}
}

func TestPenceQuotedListing(t *testing.T) {
	r := registry(t)
	res := r.ResolveListing(Query{Ticker: "BA", MIC: "XLON", Day: day})
	if res.Status != Resolved || res.Listing.Currency != "GBP" || res.Listing.QuoteUnit != "GBp" {
		t.Fatalf("%+v", res)
	}
	// Filtering by the currency GBP finds the pence line: the unit is a property of the line.
	if res := r.ResolveListing(Query{ISIN: "GB0002634946", Currency: "GBP", Day: day}); res.Status != Resolved {
		t.Errorf("%+v", res)
	}
}

func TestFundShareClassesAreSeparateSecurities(t *testing.T) {
	r := registry(t)
	set := r.Set()
	var dist, acc Security
	for _, s := range set.Securities {
		switch s.ID {
		case "sec_exw_dist":
			dist = s
		case "sec_exw_acc":
			acc = s
		}
	}
	if dist.ISIN == acc.ISIN || dist.IssuerID != acc.IssuerID || dist.Distribution != Distributing || acc.Distribution != Accumulating {
		t.Fatalf("%+v %+v", dist, acc)
	}
	res := r.ResolveSecurity(Query{ISIN: acc.ISIN, Day: day})
	if res.Status != Resolved || res.Security.Distribution != Accumulating || len(res.Candidates) != 2 {
		t.Fatalf("%+v", res)
	}
	if res := r.ResolveListing(Query{ISIN: dist.ISIN, MIC: "XETR", Day: day}); res.Status != Resolved || res.Listing.Ticker != "EXWD" || res.Security.Distribution != Distributing {
		t.Errorf("distributing on Xetra: %+v", res)
	}
	// The ticker EXWD is the distributing class only; asking for the accumulating
	// ISIN with that ticker is a conflict, not a silent pick.
	if res := r.ResolveListing(Query{ISIN: acc.ISIN, Ticker: "EXWD", Day: day}); res.Status != Conflict {
		t.Errorf("ISIN of one class with the ticker of the other: %+v", res)
	}
}

func TestDualListingIsAmbiguousUntilTheVenueIsNamed(t *testing.T) {
	r := registry(t)
	dist := "sec_exw_dist"
	var isin string
	for _, s := range r.Set().Securities {
		if s.ID == dist {
			isin = s.ISIN
		}
	}
	res := r.ResolveListing(Query{ISIN: isin, Day: day})
	if res.Status != Ambiguous || len(res.Candidates) != 2 {
		t.Fatalf("%+v", res)
	}
	why := strings.Join(res.Reasons, "\n")
	for _, want := range []string{"XETR", "XLON", "EUR", "GBp", "add a venue or a currency"} {
		if !strings.Contains(why, want) {
			t.Errorf("the explanation lacks %q:\n%s", want, why)
		}
	}
	if res.Listing != nil {
		t.Error("an ambiguous result must not carry a pick")
	}
	// A currency is enough to choose: the euro line.
	if res := r.ResolveListing(Query{ISIN: isin, Currency: "EUR", Day: day}); res.Status != Resolved || res.Listing.MIC != "XETR" {
		t.Errorf("%+v", res)
	}
	// The same ticker on two venues is two instruments: ambiguous by ticker alone.
	if res := r.ResolveListing(Query{Ticker: "EXWD", Day: day}); res.Status != Ambiguous {
		t.Errorf("EXWD is on two venues: %+v", res)
	}
	if res := r.ResolveListing(Query{Ticker: "EXWA", Day: day}); res.Status != Ambiguous {
		t.Errorf("EXWA is on Xetra and London: %+v", res)
	}
	if res := r.ResolveListing(Query{Ticker: "EXWA", Currency: "USD", Day: day}); res.Status != Resolved || res.Listing.MIC != "XLON" || res.Listing.QuoteUnit != "USD" {
		t.Errorf("EXWA in USD: %+v", res)
	}
}

func TestResolutionFailsClearly(t *testing.T) {
	r := registry(t)
	cases := []struct {
		name string
		q    Query
		want Status
	}{
		{"no identifier", Query{Day: day}, Invalid},
		{"no day", Query{Ticker: "SAP"}, Invalid},
		{"bad day", Query{Ticker: "SAP", Day: "yesterday"}, Invalid},
		{"bad ISIN", Query{ISIN: "US0378331006", Day: day}, Invalid},
		{"bad MIC", Query{Ticker: "SAP", MIC: "xetr", Day: day}, Invalid},
		{"contract without broker", Query{ConID: "123", Day: day}, Invalid},
		{"unknown ticker", Query{Ticker: "NOPE", Day: day}, NotFound},
		{"ISIN of a security that is not in the set", Query{ISIN: withCheck("US0000000AA"), Day: day}, NotFound},
		{"ISIN and ticker of two securities", Query{ISIN: "DE0007164600", Ticker: "AAPL", Day: day}, Conflict},
		{"before the listing existed", Query{Ticker: "AAPL", Day: "1970-01-01"}, NotFound},
	}
	for _, c := range cases {
		res := r.ResolveListing(c.q)
		if res.Status != c.want {
			t.Errorf("%s: %s, want %s (%v)", c.name, res.Status, c.want, res.Reasons)
		}
		if res.Status != Resolved && len(res.Reasons) == 0 {
			t.Errorf("%s: no reason given", c.name)
		}
	}
	res := r.ResolveListing(Query{ISIN: "DE0007164600", Ticker: "AAPL", Day: day})
	if len(res.Candidates) != 2 {
		t.Errorf("a conflict shows both instruments: %+v", res.Candidates)
	}
}

func TestTickerReuseOverTime(t *testing.T) {
	s := ExampleSet()
	s.Issuers = append(s.Issuers, Issuer{ID: "iss_old", Name: "Old Co", Country: "US"})
	s.Securities = append(s.Securities, Security{ID: "sec_old", IssuerID: "iss_old", Name: "Old Co common", Kind: KindStock, ISIN: withCheck("US0000000AA")})
	// Old Co used AAPL on Nasdaq before Apple, then gave it up.
	s.Listings = append(s.Listings, Listing{SecurityID: "sec_old", MIC: "XNAS", Ticker: "AAPL", Currency: "USD", QuoteUnit: "USD", From: "1970-01-01", To: "1980-12-12"})
	r, _, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	if res := r.ResolveListing(Query{Ticker: "AAPL", Day: "1975-06-01"}); res.Status != Resolved || res.Security.ID != "sec_old" {
		t.Errorf("1975: %+v", res)
	}
	if res := r.ResolveListing(Query{Ticker: "AAPL", Day: day}); res.Status != Resolved || res.Security.ID != "sec_apple" {
		t.Errorf("today: %+v", res)
	}
	// An overlap is refused.
	s.Listings[len(s.Listings)-1].To = "1990-01-01"
	if _, _, err := New(s); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Errorf("overlapping tickers: %v", err)
	}
}

func TestBrokerContractsResolveAndMustBeUnique(t *testing.T) {
	s := ExampleSet()
	s.Listings[0].Contracts = []Contract{{Broker: "ibkr", ConID: "265598", Route: "SMART"}}
	r, _, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	res := r.ResolveListing(Query{Broker: "ibkr", ConID: "265598", Day: day})
	if res.Status != Resolved || res.Listing.Ticker != "AAPL" || res.Listing.Contracts[0].Route != "SMART" {
		t.Errorf("%+v", res)
	}
	if res := r.ResolveListing(Query{Broker: "ibkr", ConID: "265598", Ticker: "SAP", Day: day}); res.Status != Conflict {
		t.Errorf("contract of one listing with the ticker of another: %+v", res)
	}
	s.Listings[1].Contracts = []Contract{{Broker: "ibkr", ConID: "265598"}}
	if _, _, err := New(s); err == nil {
		t.Error("one contract on two listings")
	}
}

func TestValidationRejectsEachDefect(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Set)
		want string
	}{
		{"missing quote unit", func(s *Set) { s.Listings[2].QuoteUnit = "" }, "quote unit is not stated"},
		{"unit of another currency", func(s *Set) { s.Listings[2].QuoteUnit = "EUR" }, "unit of EUR"},
		{"unit case", func(s *Set) { s.Listings[2].QuoteUnit = "gbp" }, "unknown quote unit"},
		{"bad currency", func(s *Set) { s.Listings[0].Currency = "US" }, "not a listed currency"},
		{"bad ISIN", func(s *Set) { s.Securities[0].ISIN = "US0378331006" }, "check digit"},
		{"duplicate ISIN", func(s *Set) { s.Securities[1].ISIN = s.Securities[0].ISIN }, "already the security"},
		{"accumulating and distributing with the same ISIN", func(s *Set) { s.Securities[4].ISIN = s.Securities[3].ISIN }, "different ISINs"},
		{"same class twice", func(s *Set) { s.Securities[4].ShareClass = "USD Dist" }, "same issuer, name and share class"},
		{"stock with a distribution", func(s *Set) { s.Securities[0].Distribution = Distributing }, "applies to funds"},
		{"unknown issuer", func(s *Set) { s.Securities[0].IssuerID = "iss_none" }, "not defined"},
		{"unknown security", func(s *Set) { s.Listings[0].SecurityID = "sec_none" }, "not defined"},
		{"bad kind", func(s *Set) { s.Securities[0].Kind = "bond" }, "kind"},
		{"fund distribution", func(s *Set) { s.Securities[3].Distribution = "sometimes" }, "distributing, accumulating or unknown"},
		{"bad from", func(s *Set) { s.Listings[0].From = "1980" }, "not a date"},
		{"to before from", func(s *Set) { s.Listings[0].To = "1980-12-11" }, "after from"},
		{"ticker with a space", func(s *Set) { s.Listings[0].Ticker = "A APL" }, "without spaces"},
		{"two primaries", func(s *Set) {
			s.Listings[3].Primary, s.Listings[4].Primary = Yes, Yes
		}, "two primary listings"},
		{"bad country", func(s *Set) { s.Issuers[0].Country = "usa" }, "two-letter"},
		{"bad LEI", func(s *Set) { s.Issuers[0].LEI = "12345678901234567890" }, "mod-97"},
		{"duplicate security id", func(s *Set) { s.Securities[1].ID = s.Securities[0].ID }, "defined twice"},
		{"empty issuer name", func(s *Set) { s.Issuers[0].Name = "" }, "no name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ExampleSet()
			tt.edit(&s)
			var found bool
			for _, p := range Errors(Validate(s)) {
				found = found || strings.Contains(p.Message, tt.want)
			}
			if !found {
				t.Errorf("no error containing %q in %v", tt.want, Validate(s))
			}
		})
	}
	// Warnings that do not block: pounds on London, an unknown venue, a missing ISIN.
	s := ExampleSet()
	s.Listings[2].QuoteUnit = "GBP"
	s.Listings = append(s.Listings, Listing{SecurityID: "sec_sap", MIC: "XZZZ", Ticker: "SAPZ", Currency: "EUR", QuoteUnit: "EUR", From: "2020-01-01"})
	s.Securities[1].ISIN = ""
	var warns []string
	for _, p := range Validate(s) {
		if p.Severity == Warning {
			warns = append(warns, p.Message)
		}
	}
	all := strings.Join(warns, "|")
	for _, want := range []string{"quote pence", "not in the table", "no ISIN"} {
		if !strings.Contains(all, want) {
			t.Errorf("warnings lack %q: %v", want, warns)
		}
	}
	if len(Errors(Validate(s))) != 0 {
		t.Errorf("those are warnings only: %v", Errors(Validate(s)))
	}
}

func TestParseRoundTripsTheExampleAndRejectsMisspeltKeys(t *testing.T) {
	b, err := Marshal(ExampleSet())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if errs := Errors(Validate(got)); len(errs) > 0 {
		t.Errorf("the round-tripped example does not validate: %v", errs)
	}
	if len(got.Listings) != len(ExampleSet().Listings) {
		t.Errorf("listings: %d", len(got.Listings))
	}
	if _, err := Parse(strings.NewReader("issuers: []\nsecurities: []\nlistings:\n  - quote_unti: GBp\n")); err == nil {
		t.Error("a misspelt key was accepted")
	}
	if _, err := Parse(strings.NewReader("")); err == nil {
		t.Error("empty file")
	}
}
