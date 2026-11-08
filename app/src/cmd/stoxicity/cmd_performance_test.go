package main

import (
	"strings"
	"testing"
)

func TestPerformanceReportsGainApartFromDepositsAndRefusesAdjustedMarks(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "instruments", "import", "-file", writeInstruments(t, e), "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "10000.00 USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-11-02", "-security", "sec_apple", "-qty", "10", "-gross", "1000.00 USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-04", "-amount", "5000.00 USD").wantCode(t, exitOK)

	marks := writeFile(t, e, "marks.csv", "security,day,open,high,low,close,volume,currency\n"+
		"sec_apple,2026-11-02,100,100,100,100,1,USD\n"+
		"sec_apple,2026-11-03,110,110,110,110,1,USD\n"+
		"sec_apple,2026-11-04,110,110,110,110,1,USD\n"+
		"fund,2026-11-02,10,10,10,10,1,USD\n"+
		"fund,2026-11-04,11,11,11,11,1,USD\n")
	bench := writeFile(t, e, "fund.json", `{"name":"world fund","kind":"fund","security":"fund"}`)

	r := e.run(t, "performance", "main/core", "-marks", marks, "-to", "2026-11-04", "-benchmark", bench)
	r.wantCode(t, exitOK)
	// 10 shares up 10 is 100.00 of gain; the 5,000.00 deposit is not gain
	r.wantOut(t, "gain", "100.00 USD", "paid in", "15000.00 USD", "unrealised change", "time-weighted return", "world fund", "gross of trading costs")
	r = e.run(t, "performance", "main/core", "-marks", marks, "-to", "2026-11-04", "-json")
	r.wantCode(t, exitOK)
	r.wantOut(t, `"gain"`, `"twr"`, `"conventions"`, `"amount": "100.00"`)

	// without prices nothing is invented
	r = e.run(t, "performance", "main/core", "-to", "2026-11-04")
	r.wantCode(t, exitOK)
	r.wantOut(t, "unavailable", "no price for sec_apple")
	if strings.Contains(r.out, "0.00 USD") && strings.Contains(r.out, "gain  ") {
		t.Fatalf("a gain of zero was shown for a missing price:\n%s", r.out)
	}

	e.run(t, "performance", "main/core", "-adjusted", "-marks", marks).wantCode(t, exitUsage)
	e.run(t, "performance").wantCode(t, exitUsage)
}
