package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeInstruments(t *testing.T, e *testEnv) string {
	t.Helper()
	r := e.run(t, "instruments", "example")
	r.wantCode(t, exitOK)
	path := filepath.Join(e.dir, "instruments.yaml")
	if err := os.WriteFile(path, []byte(r.out), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstrumentsWorkflow(t *testing.T) {
	e := newTestEnv(t)
	file := writeInstruments(t, e)

	r := e.run(t, "instruments", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no instruments imported yet")

	r = e.run(t, "instruments", "check", "-file", file)
	r.wantCode(t, exitOK)
	r.wantOut(t, "5 securities", "7 listings")

	r = e.run(t, "instruments", "import", "-file", file)
	r.wantCode(t, exitUsage)
	r = e.run(t, "instruments", "import", "-file", file, "-source", "fixture")
	r.wantCode(t, exitOK)
	r.wantOut(t, "securities", "Nothing here is verified")
	r = e.run(t, "instruments", "import", "-file", file, "-source", "fixture")
	r.wantCode(t, exitOK)
	if strings.Contains(r.out, "revised  1") {
		t.Errorf("an unchanged import revised something:\n%s", r.out)
	}

	r = e.run(t, "instruments", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "AAPL", "XLON", "GBp", "US0378331005", "distributing", "accumulating")

	// A subunit quote resolves with its unit.
	r = e.run(t, "instruments", "resolve", "-mic", "XLON", "-ticker", "BA", "-day", "2026-06-01")
	r.wantCode(t, exitOK)
	r.wantOut(t, "resolved", "GBp", "GBP")

	// A cross-listed fund is ambiguous until the venue is named, and says so.
	r = e.run(t, "instruments", "resolve", "-ticker", "EXWD", "-day", "2026-06-01")
	r.wantCode(t, exitError)
	r.wantOut(t, "ambiguous", "XETR", "XLON")
	r = e.run(t, "instruments", "resolve", "-ticker", "EXWD", "-mic", "XETR", "-day", "2026-06-01")
	r.wantCode(t, exitOK)
	r.wantOut(t, "resolved", "distributing")

	// A missing day is refused: tickers are reused.
	r = e.run(t, "instruments", "resolve", "-ticker", "AAPL")
	r.wantCode(t, exitError)
	r.wantOut(t, "day")
}

func TestInstrumentsCheckRefusesABadFile(t *testing.T) {
	e := newTestEnv(t)
	bad := filepath.Join(e.dir, "bad.yaml")
	os.WriteFile(bad, []byte("issuers: []\nsecurities: []\nlistings:\n  - security: x\n    mic: XLON\n    ticker: BA\n    currency: GBP\n    from: 2020-01-01\n"), 0o600)
	r := e.run(t, "instruments", "check", "-file", bad)
	r.wantCode(t, exitError)
	r.wantOut(t, "quote")
	r = e.run(t, "instruments", "import", "-file", bad, "-source", "x")
	r.wantCode(t, exitError)
}

func TestSourcesCapabilitiesOpensNoStoreAndPromisesNothing(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "sources", "capabilities")
	r.wantCode(t, exitOK)
	r.wantOut(t, "never", "unknown means not checked", "-allow-paid")
	if _, err := os.Stat(filepath.Join(e.dir, "data", "base.db")); err == nil {
		t.Error("sources capabilities created the database")
	}
	r = e.run(t, "sources", "capabilities", "-data", "prices_eod", "-market", "DE", "-kind", "stock")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no usable provider", "polygon")
	r = e.run(t, "sources", "capabilities", "-data", "tips")
	r.wantCode(t, exitUsage)
	r = e.run(t, "sources", "capabilities", "-market", "DE")
	r.wantCode(t, exitUsage)
}
