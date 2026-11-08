package main

import "testing"

func TestIncomeWorkflow(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "10000.00 USD").wantCode(t, exitOK)

	// nothing held yet, nothing known
	r := e.run(t, "income", "expected", "main/core")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no declared or estimated payout")

	e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-11-03", "-security", "sec_apple", "-qty", "10", "-gross", "1500.00 USD").wantCode(t, exitOK)
	// bought on the ex-date: does not earn the next payout
	e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-11-10", "-security", "sec_apple", "-qty", "5", "-gross", "750.00 USD").wantCode(t, exitOK)

	div := writeFile(t, e, "div.csv", divHeader+
		"ordinary,2026Q4,declared,0.25,USD,2026-11-10,2026-11-10,2026-11-13,XNAS,AAPL\n"+
		"ordinary,2027Q1,estimated,0.25,USD,2027-02-09,,,XNAS,AAPL\n"+
		"special,2026,declared,1.00,USD,2026-11-10,,,XNAS,AAPL\n")
	e.run(t, "dividends", "import", "-file", div, "-source", "company").wantCode(t, exitOK)

	// before the ex-date: expected, not bookable
	r = e.run(t, "income", "expected", "main/core", "-as-of", "2026-11-09")
	r.wantCode(t, exitOK)
	r.wantOut(t, "sec_apple|ordinary|2026Q4", "pending", "2.50 USD", "estimate", "unsupported", "gross, before any tax")
	r = e.run(t, "income", "accrue", "main/core", "-as-of", "2026-11-09")
	r.wantOut(t, "nothing to accrue")

	// after it: the 10 shares held before the ex-date earn, the 5 bought on it do not
	r = e.run(t, "income", "check", "main/core", "-as-of", "2026-11-11")
	r.wantCode(t, exitError)
	r.wantOut(t, "missing accrual", "2.50 USD", "nothing is booked")
	r = e.run(t, "income", "accrue", "main/core", "-as-of", "2026-11-11")
	r.wantCode(t, exitOK)
	r.wantOut(t, "accrued sec_apple|ordinary|2026Q4 2.50 USD")
	r = e.run(t, "income", "accrue", "main/core", "-as-of", "2026-11-11")
	r.wantOut(t, "skipped sec_apple|ordinary|2026Q4 (already booked)")
	e.run(t, "income", "check", "main/core", "-as-of", "2026-11-12").wantCode(t, exitOK)

	// overdue until the payment is recorded
	r = e.run(t, "income", "check", "main/core", "-as-of", "2026-11-20")
	r.wantCode(t, exitError)
	r.wantOut(t, "payment overdue")

	// the accrual cannot be booked a second way, and the payment needs the right form
	r = e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-13", "-gross", "2.50 USD")
	r.wantCode(t, exitError)
	r.wantErr(t, "already accrued")
	r = e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-13", "-withheld", "2.50 USD")
	r.wantCode(t, exitError)
	r.wantErr(t, "less than the gross")
	r = e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-13", "-withheld", "0.38 USD", "-import-id", "pay1")
	r.wantCode(t, exitOK)
	r.wantOut(t, "recorded")
	// repeating the same record changes nothing
	r = e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-13", "-withheld", "0.38 USD", "-import-id", "pay1")
	r.wantCode(t, exitOK)
	r = e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-14")
	r.wantCode(t, exitError)
	r.wantErr(t, "already")
	e.run(t, "income", "check", "main/core", "-as-of", "2026-11-20").wantCode(t, exitOK)

	r = e.run(t, "portfolio", "show", "main/core")
	r.wantOut(t, "10002.12 USD", "the books balance")
	r = e.run(t, "portfolio", "verify", "main/core")
	r.wantCode(t, exitOK)

	// usage errors
	e.run(t, "income").wantCode(t, exitUsage)
	e.run(t, "income", "nope").wantCode(t, exitUsage)
	e.run(t, "income", "expected").wantCode(t, exitUsage)
	e.run(t, "income", "expected", "main/core", "-as-of", "yesterday").wantCode(t, exitUsage)
	e.run(t, "income", "receive", "main/core", "-payout", "x").wantCode(t, exitUsage)
	e.run(t, "income", "receive", "main/core", "-payout", "x", "-date", "2026-11-13", "-security", "sec_apple").wantCode(t, exitUsage)
	r = e.run(t, "income", "receive", "main/core", "-payout", "nothing|ordinary|2026", "-date", "2026-11-13")
	r.wantCode(t, exitError)
	r.wantErr(t, "has not been accrued")
}

func TestIncomeReinvestedPayout(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "10000.00 USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-11-03", "-security", "sec_apple", "-qty", "10", "-gross", "1500.00 USD").wantCode(t, exitOK)
	div := writeFile(t, e, "div.csv", divHeader+"ordinary,2026Q4,declared,0.25,USD,2026-11-10,,,XNAS,AAPL\n")
	e.run(t, "dividends", "import", "-file", div, "-source", "company").wantCode(t, exitOK)
	e.run(t, "income", "accrue", "main/core", "-as-of", "2026-11-11").wantCode(t, exitOK)
	r := e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-13", "-security", "sec_apple", "-qty", "0.016")
	r.wantCode(t, exitOK)
	r = e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-14")
	r.wantCode(t, exitError)
	e.run(t, "income", "check", "main/core", "-as-of", "2026-11-20").wantCode(t, exitOK)
	e.run(t, "income", "receive", "main/core", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-14", "-security", "sec_apple").wantCode(t, exitUsage)
}
