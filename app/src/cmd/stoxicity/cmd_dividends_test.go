package main

import (
	"testing"
)

const divHeader = "type,period,status,amount,currency,ex_day,record_day,pay_day,mic,ticker\n"

func TestDividendsWorkflow(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	r := e.run(t, "dividends", "calendar")
	r.wantCode(t, exitError)
	r.wantErr(t, "instruments import")
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)

	r = e.run(t, "dividends", "calendar")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no payouts in range")

	company := writeFile(t, e, "div.csv", divHeader+
		"ordinary,2025Q3,declared,0.25,USD,2025-08-11,2025-08-11,2025-08-14,XNAS,AAPL\n"+
		"ordinary,2025Q4,declared,0.25,USD,2025-11-10,2025-11-10,2025-11-13,XNAS,AAPL\n"+
		"ordinary,2026Q1,declared,0.25,USD,2026-02-09,2026-02-09,2026-02-12,XNAS,AAPL\n"+
		"ordinary,2026Q2,declared,0.25,USD,2026-05-11,,,XNAS,AAPL\n"+
		"ordinary,2026Q3,declared,0.10,USD,2026-08-10,2026-08-10,2026-08-13,XNAS,AAPL\n"+
		"special,2026,declared,1.00,USD,2026-03-02,,,XNAS,AAPL\n"+
		"ordinary,2026Q4,estimated,0.10,USD,2026-11-09,,,XNAS,AAPL\n")
	r = e.run(t, "dividends", "import", "-file", company, "-source", "company")
	r.wantCode(t, exitOK)
	r.wantOut(t, "7 observations read, 7 new", "Neither is cash")
	r = e.run(t, "dividends", "import", "-file", company, "-source", "company")
	r.wantOut(t, "0 new, 7 already stored")

	// Another source disagrees about one ex-date.
	other := writeFile(t, e, "div2.csv", divHeader+"ordinary,2026Q2,declared,0.25,USD,2026-05-12,,,XNAS,AAPL\n")
	e.run(t, "dividends", "import", "-file", other, "-source", "aggregator").wantCode(t, exitOK)

	r = e.run(t, "dividends", "calendar", "-ticker", "AAPL", "-mic", "XNAS", "-from", "2026-01-01", "-to", "2026-12-31")
	r.wantCode(t, exitOK)
	r.wantOut(t, "conflicting", "ex-date differs", "estimated", "not cash", "never added", "1 conflicting")

	// Quality: the cut is reported, and without splits the report says it may be a split.
	r = e.run(t, "dividends", "quality", "-ticker", "AAPL", "-mic", "XNAS", "-day", "2026-12-01")
	r.wantCode(t, exitOK)
	r.wantOut(t, "quarterly", "split history not supplied", "a cut may be a share split", "overdue")

	splits := writeFile(t, e, "splits.csv", "security,day,from,to\nsec_apple,2026-06-01,1,2\n")
	r = e.run(t, "dividends", "quality", "-ticker", "AAPL", "-mic", "XNAS", "-day", "2026-12-01", "-splits", splits, "-sector", "technology", "-fcf-per-share", "2.00", "-fcf-currency", "USD")
	r.wantCode(t, exitOK)
	r.wantOut(t, "computed", "cut: 0.125 -> 0.10 USD (-0.2000)", "estimated, not declared", "not cash received and not a forecast")
	r = e.run(t, "dividends", "quality", "-ticker", "AAPL", "-mic", "XNAS", "-day", "2026-12-01", "-sector", "reit")
	r.wantOut(t, "unsupported_sector", "funds from operations")

	// An accumulating class pays no cash distribution: the file is refused; quality says so by design.
	acc := writeFile(t, e, "acc.csv", divHeader+"ordinary,2026Q3,declared,0.40,EUR,2026-09-10,,,XETR,EXWA\n")
	r = e.run(t, "dividends", "import", "-file", acc, "-source", "x")
	r.wantCode(t, exitError)
	r.wantErr(t, "accumulating")
	r = e.run(t, "dividends", "quality", "-ticker", "EXWA", "-mic", "XETR", "-day", "2026-12-01")
	r.wantOut(t, "none_by_design")
	r = e.run(t, "dividends", "quality", "-ticker", "EXWD", "-mic", "XETR", "-day", "2026-12-01")
	r.wantOut(t, "unknown", "not evidence")

	r = e.run(t, "dividends", "quality", "-ticker", "AAPL")
	r.wantCode(t, exitUsage)
}
