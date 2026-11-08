package quotes

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
)

var t0 = time.Date(2026, 11, 6, 17, 0, 0, 0, time.UTC)

// An invented document in the shape of the ECB's: two dates, one currency this
// system does not handle.
const ecbFixture = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
<gesmes:subject>Reference rates</gesmes:subject>
<gesmes:Sender><gesmes:name>European Central Bank</gesmes:name></gesmes:Sender>
<Cube>
<Cube time="2026-11-06"><Cube currency="USD" rate="1.0850"/><Cube currency="GBP" rate="0.8412"/><Cube currency="JPY" rate="163.20"/><Cube currency="IDR" rate="17000.5"/></Cube>
<Cube time="2026-11-05"><Cube currency="USD" rate="1.0900"/><Cube currency="GBP" rate="0.8400"/></Cube>
</Cube>
</gesmes:Envelope>`

func fx(base, quote, v, day, source string, seq int64) FXObs {
	return FXObs{Seq: seq, Base: money.Currency(base), Quote: money.Currency(quote), Value: money.MustDecimal(v),
		Basis: money.BasisReference, Day: day, Source: source, Received: t0.Add(time.Duration(seq) * time.Hour)}
}

func TestParseECBKeepsExactRatesAndCountsWhatItSkips(t *testing.T) {
	obs, rep, err := ParseECB(strings.NewReader(ecbFixture), "fixture", t0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Days != 2 || rep.Skipped["IDR"] != 1 || len(obs) != 5 {
		t.Fatalf("%+v, %d observations", rep, len(obs))
	}
	for _, o := range obs {
		if o.Base != "EUR" || o.Basis != money.BasisReference || o.Source != ECBSource || o.Received != t0 {
			t.Errorf("%+v", o)
		}
		if o.Basis.Executable() {
			t.Error("a reference rate must never be executable")
		}
	}
	var usd FXObs
	for _, o := range obs {
		if o.Quote == "USD" && o.Day == "2026-11-06" {
			usd = o
		}
	}
	if usd.Value.String() != "1.0850" {
		t.Errorf("the scale of the published rate was lost: %s", usd.Value)
	}
	for _, bad := range []string{"", "<x/>", strings.Replace(ecbFixture, `rate="1.0850"`, `rate="1.08e0"`, 1), strings.Replace(ecbFixture, `time="2026-11-06"`, `time="06/11/2026"`, 1)} {
		if _, _, err := ParseECB(strings.NewReader(bad), "x", t0); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestResolveFXAgeConflictAndRevision(t *testing.T) {
	obs := []FXObs{fx("EUR", "USD", "1.0900", "2026-11-05", "ecb", 1), fx("EUR", "USD", "1.0850", "2026-11-06", "ecb", 2)}

	a := ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06"})
	if a.Verdict != OK || a.Rate.Value.String() != "1.0850" || a.AgeDays != 0 || a.Rate.Basis.Executable() {
		t.Fatalf("%+v", a)
	}
	// Sunday: the Friday rate is two days old. The caller decides what is too old.
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-08", MaxAgeDays: 3})
	if a.Verdict != OK || a.Day != "2026-11-06" || a.AgeDays != 2 {
		t.Fatalf("%+v", a)
	}
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-08", MaxAgeDays: 1})
	if a.Verdict != Stale || a.Rate != nil || !strings.Contains(a.Reasons[0], "2 day(s)") {
		t.Fatalf("%+v", a)
	}
	if a = ResolveFX(obs, "EUR", "GBP", Need{Day: "2026-11-06"}); a.Verdict != Missing {
		t.Errorf("no such pair: %+v", a)
	}
	if a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-04"}); a.Verdict != Missing || !strings.Contains(strings.Join(a.Reasons, " "), "dated after") {
		t.Errorf("a rate from the future must not be used: %+v", a)
	}

	// Two sources disagree about one day.
	obs = append(obs, fx("EUR", "USD", "1.0852", "2026-11-06", "other", 3))
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06"})
	if a.Verdict != Conflicting || a.Rate != nil || len(a.Candidates) != 2 {
		t.Fatalf("%+v", a)
	}
	// The same source correcting itself is a revision, not a conflict.
	obs = append(obs, fx("EUR", "USD", "1.0850", "2026-11-06", "other", 4))
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06"})
	if a.Verdict != OK || a.Rate.Value.String() != "1.0850" || len(a.Sources) != 2 {
		t.Fatalf("%+v", a)
	}
	// As of a time before the correction the conflict is what was known.
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06", KnownAt: t0.Add(3 * time.Hour)})
	if a.Verdict != Conflicting {
		t.Errorf("as-of: %+v", a)
	}
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06", KnownAt: t0})
	if a.Verdict != Missing || !strings.Contains(strings.Join(a.Reasons, " "), "received after") {
		t.Errorf("before anything was received: %+v", a)
	}
}

func TestResolveFXDirectionAndBasis(t *testing.T) {
	obs := []FXObs{fx("EUR", "USD", "1.0850", "2026-11-06", "ecb", 1)}
	a := ResolveFX(obs, "USD", "EUR", Need{Day: "2026-11-06"})
	if a.Verdict != OK || !a.Inverted || a.Rate.Base != "USD" || a.Rate.Value.String() != "0.921658986175" {
		t.Fatalf("%+v", a)
	}
	// A reference rate is never offered as a fill, and a fill never as a reference.
	if a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06", Basis: money.BasisFill}); a.Verdict != Missing || !strings.Contains(strings.Join(a.Reasons, " "), "another basis") {
		t.Errorf("%+v", a)
	}
	fill := fx("EUR", "USD", "1.0801", "2026-11-06", "ibkr-statement", 2)
	fill.Basis, fill.SourceRef = money.BasisFill, "statement 2026-11-06 line 4"
	obs = append(obs, fill)
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06"})
	if a.Verdict != OK || a.Rate.Value.String() != "1.0850" {
		t.Errorf("a fill leaked into a reference answer: %+v", a)
	}
	a = ResolveFX(obs, "EUR", "USD", Need{Day: "2026-11-06", Basis: money.BasisFill})
	if a.Verdict != OK || a.Rate.Value.String() != "1.0801" || !a.Rate.Basis.Executable() {
		t.Errorf("%+v", a)
	}
	// No cross rate is derived through a third currency.
	obs = []FXObs{fx("EUR", "USD", "1.0850", "2026-11-06", "ecb", 1), fx("EUR", "GBP", "0.8412", "2026-11-06", "ecb", 2)}
	if a = ResolveFX(obs, "USD", "GBP", Need{Day: "2026-11-06"}); a.Verdict != Missing {
		t.Errorf("%+v", a)
	}
}

func TestFXObsValidation(t *testing.T) {
	good := fx("EUR", "USD", "1.0850", "2026-11-06", "ecb", 0)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*FXObs){
		"zero":        func(o *FXObs) { o.Value = money.Int(0) },
		"negative":    func(o *FXObs) { o.Value = money.MustDecimal("-1.2") },
		"same":        func(o *FXObs) { o.Quote = "EUR" },
		"no source":   func(o *FXObs) { o.Source = " " },
		"bad day":     func(o *FXObs) { o.Day = "2026-13-01" },
		"fill no ref": func(o *FXObs) { o.Basis = money.BasisFill },
		"no basis":    func(o *FXObs) { o.Basis = "" },
	}
	for name, edit := range bad {
		o := good
		edit(&o)
		if o.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func listing(t *testing.T, mic, ticker string) (instrument.Listing, *instrument.Registry) {
	t.Helper()
	reg, _, err := instrument.New(instrument.ExampleSet())
	if err != nil {
		t.Fatal(err)
	}
	l, err := RegistryResolver(reg)(mic, ticker, "2026-11-06")
	if err != nil {
		t.Fatal(err)
	}
	return l, reg
}

func px(l instrument.Listing, v, unit, day, source string, seq int64) PriceObs {
	u, _ := money.ParseQuoteUnit(unit)
	p, _ := money.NewPrice(money.MustDecimal(v), u)
	return PriceObs{Seq: seq, ListingID: l.ID, Day: day, Kind: Close, Price: p, UnitBasis: UnitStated, Adjustment: Raw, Delay: EndOfDay,
		Source: source, Received: t0.Add(time.Duration(seq) * time.Hour)}
}

func TestResolvePriceRefusesWrongUnitsStaleAndConflicting(t *testing.T) {
	ba, _ := listing(t, "XLON", "BA")
	if ba.QuoteUnit != "GBp" {
		t.Fatal(ba.QuoteUnit)
	}
	need := Need{Day: "2026-11-06", RequireStatedUnit: true}

	a := ResolvePrice([]PriceObs{px(ba, "1234.5", "GBp", "2026-11-06", "file", 1)}, ba, need)
	if a.Verdict != OK || a.Price.Unit.Code != "GBp" {
		t.Fatalf("%+v", a)
	}
	maj, _ := a.Price.Major()
	if maj.String() != "12.345" {
		t.Errorf("pence to pounds: %s", maj)
	}

	// 12.345 labelled GBP on a GBp line is the factor-of-100 mistake: refused.
	a = ResolvePrice([]PriceObs{px(ba, "12.345", "GBP", "2026-11-06", "file", 1)}, ba, need)
	if a.Verdict != UnitAmbiguous || a.Price != nil || !strings.Contains(a.Reasons[0], "quoted in GBp") {
		t.Fatalf("%+v", a)
	}

	// A unit taken from the instrument record is fine to look at and refused for sizing money.
	o := px(ba, "1234.5", "GBp", "2026-11-06", "file", 1)
	o.UnitBasis = UnitFromListing
	if a = ResolvePrice([]PriceObs{o}, ba, Need{Day: "2026-11-06"}); a.Verdict != OK || a.UnitBasis != UnitFromListing || len(a.Reasons) == 0 {
		t.Errorf("%+v", a)
	}
	if a = ResolvePrice([]PriceObs{o}, ba, need); a.Verdict != UnitAmbiguous {
		t.Errorf("%+v", a)
	}

	// Stale.
	old := px(ba, "1200", "GBp", "2026-10-30", "file", 1)
	if a = ResolvePrice([]PriceObs{old}, ba, Need{Day: "2026-11-06", MaxAgeDays: 4}); a.Verdict != Stale || a.AgeDays != 7 {
		t.Errorf("%+v", a)
	}
	// Conflict between two sources; the same source correcting itself is a revision.
	obs := []PriceObs{px(ba, "1234.5", "GBp", "2026-11-06", "a", 1), px(ba, "1235.0", "GBp", "2026-11-06", "b", 2)}
	if a = ResolvePrice(obs, ba, need); a.Verdict != Conflicting {
		t.Errorf("%+v", a)
	}
	obs = append(obs, px(ba, "1234.5", "GBp", "2026-11-06", "b", 3))
	if a = ResolvePrice(obs, ba, need); a.Verdict != OK {
		t.Errorf("%+v", a)
	}
	if a = ResolvePrice(obs, ba, Need{Day: "2026-11-06", KnownAt: t0.Add(2 * time.Hour), RequireStatedUnit: true}); a.Verdict != Conflicting {
		t.Errorf("as of before the correction: %+v", a)
	}
}

func TestResolvePriceKeepsAdjustmentsApart(t *testing.T) {
	ba, _ := listing(t, "XLON", "BA")
	adj := px(ba, "1100", "GBp", "2026-11-06", "vendor", 1)
	adj.Adjustment = SplitAdjusted
	obs := []PriceObs{adj}
	a := ResolvePrice(obs, ba, Need{Day: "2026-11-06"})
	if a.Verdict != Missing || !strings.Contains(strings.Join(a.Reasons, " "), "another adjustment") {
		t.Fatalf("a restated price must not stand in for a raw one: %+v", a)
	}
	if a = ResolvePrice(obs, ba, Need{Day: "2026-11-06", Adjustment: SplitAdjusted}); a.Verdict != OK {
		t.Errorf("%+v", a)
	}
	if err := (PriceObs{}).Validate(); err == nil {
		t.Error("an empty observation validated")
	}
}

func TestPriceCSVStatesOrBorrowsTheUnit(t *testing.T) {
	_, reg := listing(t, "XLON", "BA")
	file := "mic,ticker,day,price,unit,adjustment,delay\n" +
		"XLON,BA,2026-11-06,1234.5,GBp,raw,eod\n" +
		"XNAS,AAPL,2026-11-06,230.10,,raw,eod\n"
	obs, err := ParsePriceCSV(strings.NewReader(file), RegistryResolver(reg), "file", Hash([]byte(file)), t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 || obs[0].UnitBasis != UnitStated || obs[1].UnitBasis != UnitFromListing || obs[1].Price.Unit.Code != "USD" {
		t.Fatalf("%+v", obs)
	}
	if !strings.HasPrefix(obs[0].SourceRef, "sha256:") {
		t.Error(obs[0].SourceRef)
	}
	cases := map[string]string{
		"lower-case unit": "mic,ticker,day,price,unit\nXLON,BA,2026-11-06,12.3,gbp\n",
		"unknown column":  "mic,ticker,day,price,units\nXLON,BA,2026-11-06,12.3,GBp\n",
		"float exponent":  "mic,ticker,day,price\nXLON,BA,2026-11-06,1e3\n",
		"negative":        "mic,ticker,day,price\nXLON,BA,2026-11-06,-1\n",
		"no such listing": "mic,ticker,day,price\nXLON,NOPE,2026-11-06,1\n",
		"ambiguous":       "mic,ticker,day,price\n,EXWD,2026-11-06,1\n",
		"bad day":         "mic,ticker,day,price\nXLON,BA,6 Nov,1\n",
		"no rows":         "mic,ticker,day,price\n",
		"short row":       "mic,ticker,day,price\nXLON,BA,2026-11-06\n",
	}
	for name, body := range cases {
		if _, err := ParsePriceCSV(strings.NewReader(body), RegistryResolver(reg), "file", "ref", t0); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestFXCSVDefaultsToAUserAssumption(t *testing.T) {
	obs, err := ParseFXCSV(strings.NewReader("base,quote,day,rate\nEUR,USD,2026-11-06,1.09\n"), "me", "sha256:x", t0)
	if err != nil || len(obs) != 1 || obs[0].Basis != money.BasisUser {
		t.Fatalf("%+v %v", obs, err)
	}
	if _, err := ParseFXCSV(strings.NewReader("base,quote,day,rate,basis\nEUR,USD,2026-11-06,1.09,fill\n"), "me", "sha256:x", t0); err != nil {
		t.Errorf("a fill with the file as its reference: %v", err)
	}
	if _, err := ParseFXCSV(strings.NewReader("base,quote,day,rate\nEUR,eur,2026-11-06,1.09\n"), "me", "r", t0); err == nil {
		t.Error("lower case")
	}
}
