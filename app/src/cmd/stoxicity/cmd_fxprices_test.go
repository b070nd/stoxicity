package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, e *testEnv, name, body string) string {
	t.Helper()
	p := filepath.Join(e.dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFXWorkflow(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "fx", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no exchange rates stored yet")

	url := e.server.URL + "/ecb/daily.xml"
	r = e.run(t, "fx", "fetch", "-url", url)
	r.wantCode(t, exitOK)
	r.wantOut(t, "3 rates read, 3 new", "IDR", "not rates anything was or can be traded at")
	r = e.run(t, "fx", "fetch", "-url", url)
	r.wantCode(t, exitOK)
	r.wantOut(t, "0 new, 3 already stored")

	r = e.run(t, "fx", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "EUR/USD", "reference", "ecb", "2026-11-05", "2026-11-06")

	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-06")
	r.wantCode(t, exitOK)
	r.wantOut(t, "1 EUR = 1.0850 USD", "reference rate", "not a rate anything was or can be traded at")

	// Sunday: stale unless the caller allows the age.
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-08")
	r.wantCode(t, exitError)
	r.wantOut(t, "stale", "2 day(s) before")
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-08", "-max-age", "3")
	r.wantCode(t, exitOK)

	// The other direction is inverted and says so.
	r = e.run(t, "fx", "rate", "-from", "USD", "-to", "EUR", "-day", "2026-11-06")
	r.wantCode(t, exitOK)
	r.wantOut(t, "inverted from EUR/USD", "half-even")

	// A fill is a different thing and is missing.
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-06", "-basis", "fill")
	r.wantCode(t, exitError)
	r.wantOut(t, "missing", "another basis")

	// A second source that disagrees makes the day conflicting.
	f := writeFile(t, e, "fx.csv", "base,quote,day,rate\nEUR,USD,2026-11-06,1.0999\n")
	r = e.run(t, "fx", "import", "-file", f, "-source", "my-notes")
	r.wantCode(t, exitOK)
	r.wantOut(t, "1 new", "user assumption")
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-06")
	r.wantCode(t, exitOK) // a user assumption is another basis; the reference answer is unchanged
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD", "-day", "2026-11-06", "-basis", "user")
	r.wantCode(t, exitOK)
	r.wantOut(t, "1.0999", "user")

	// Input is refused, not repaired.
	r = e.run(t, "fx", "rate", "-from", "eur", "-to", "USD", "-day", "2026-11-06")
	r.wantCode(t, exitUsage)
	r = e.run(t, "fx", "rate", "-from", "EUR", "-to", "USD")
	r.wantCode(t, exitUsage)
	r = e.run(t, "fx", "import", "-file", writeFile(t, e, "bad.csv", "base,quote,day,rate\nEUR,USD,2026-11-06,1e0\n"), "-source", "x")
	r.wantCode(t, exitError)
}

func TestPricesWorkflow(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	r := e.run(t, "prices", "import", "-file", writeFile(t, e, "p.csv", "mic,ticker,day,price\nXLON,BA,2026-11-06,1\n"), "-source", "x")
	r.wantCode(t, exitError)
	r.wantErr(t, "instruments import")
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)

	p := writeFile(t, e, "p.csv", "mic,ticker,day,price,unit,adjustment,delay\n"+
		"XLON,BA,2026-11-06,1234.5,GBp,raw,eod\n"+
		"XNAS,AAPL,2026-11-06,230.10,,raw,eod\n")
	r = e.run(t, "prices", "import", "-file", p, "-source", "my-broker-export")
	r.wantCode(t, exitOK)
	r.wantOut(t, "2 prices read, 2 new", "stated no unit")

	r = e.run(t, "prices", "at", "-mic", "XLON", "-ticker", "BA", "-day", "2026-11-06", "-require-stated-unit")
	r.wantCode(t, exitOK)
	r.wantOut(t, "1234.5 GBp", "= 12.345 GBP")

	// A unit borrowed from the instrument record is shown, and refused for sizing money.
	r = e.run(t, "prices", "at", "-mic", "XNAS", "-ticker", "AAPL", "-day", "2026-11-06")
	r.wantCode(t, exitOK)
	r.wantOut(t, "230.10 USD", "unit listing")
	r = e.run(t, "prices", "at", "-mic", "XNAS", "-ticker", "AAPL", "-day", "2026-11-06", "-require-stated-unit")
	r.wantCode(t, exitError)
	r.wantOut(t, "unit_ambiguous")

	// Pounds stated for a pence line is the classic factor of 100: refused at import.
	bad := writeFile(t, e, "bad.csv", "mic,ticker,day,price,unit\nXLON,BA,2026-11-07,12.345,gbp\n")
	e.run(t, "prices", "import", "-file", bad, "-source", "x").wantCode(t, exitError)
	wrong := writeFile(t, e, "wrong.csv", "mic,ticker,day,price,unit,adjustment\nXLON,BA,2026-11-09,12.345,GBP,raw\n")
	e.run(t, "prices", "import", "-file", wrong, "-source", "wrong-unit").wantCode(t, exitOK)
	r = e.run(t, "prices", "at", "-mic", "XLON", "-ticker", "BA", "-day", "2026-11-09")
	r.wantCode(t, exitError)
	r.wantOut(t, "unit_ambiguous", "quoted in GBp")

	// Stale and missing.
	r = e.run(t, "prices", "at", "-mic", "XNAS", "-ticker", "AAPL", "-day", "2026-11-12", "-max-age", "3")
	r.wantCode(t, exitError)
	r.wantOut(t, "stale")
	r = e.run(t, "prices", "at", "-mic", "XETR", "-ticker", "SAP", "-day", "2026-11-06")
	r.wantCode(t, exitError)
	r.wantOut(t, "missing")

	// An ambiguous instrument is not guessed at.
	r = e.run(t, "prices", "at", "-ticker", "EXWD", "-day", "2026-11-06")
	r.wantCode(t, exitError)
	r.wantOut(t, "ambiguous")
	if !strings.Contains(r.errOut, "ambiguous") {
		t.Errorf("stderr: %s", r.errOut)
	}
}
