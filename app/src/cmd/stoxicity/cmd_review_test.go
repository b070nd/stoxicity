package main

import (
	"strings"
	"testing"
)

func reviewFixture(t *testing.T) (*testEnv, string, string) {
	t.Helper()
	e := allocateEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-08-30", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-08-30", "-amount", "5000.00 USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-09-01", "-security", "sec_apple", "-qty", "100", "-gross", "100.00 USD", "-import-id", "buy1").wantCode(t, exitOK)
	activatePolicy(t, e, func(s string) string {
		s = strings.Replace(s, "max_issuer: \"0.10\"", "max_issuer: \"0.30\"", 1)
		return strings.Replace(s, "max_sector: \"0.25\"", "max_sector: \"0.40\"", 1)
	})
	prices := writeFile(t, e, "prices.csv", candHeader+"sec_apple,,,technology,1.00 USD,2026-11-02,no,,,yes,\n")
	return e, "main/core", prices
}

func TestReviewHoldings(t *testing.T) {
	e, book, prices := reviewFixture(t)
	run := func(extra ...string) result {
		return e.run(t, append([]string{"review", "run", book, "-prices", prices, "-as-of", "2026-11-03", "-rates", "USD=0.92"}, extra...)...)
	}
	e.run(t, "review").wantCode(t, exitUsage)
	r := run()
	r.wantCode(t, exitOK)
	r.wantOut(t, "sec_apple", "HOLD (review required)", "thesis_unrecorded", "not evaluated: scheduled rebalance (rule 6)", "a review date is not a sale date", "nothing is placed")

	// the thesis is yours to record, and a status needs a source
	e.run(t, "review", "thesis", "set", "-security", "sec_apple", "-status", "intact", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "review", "thesis", "set", "-security", "sec_apple", "-status", "great", "-note", "n", "-source", "memo", "-ref", "1", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "review", "thesis", "show").wantOut(t, "no thesis is recorded")
	e.run(t, "review", "thesis", "set", "-security", "sec_apple", "-status", "intact", "-note", "Cash generation unchanged", "-source", "memo", "-ref", "memo 3", "-by", "me", "-day", "2026-11-02").wantOut(t, "recorded as intact")
	e.run(t, "review", "thesis", "show").wantOut(t, "sec_apple", "intact", "Cash generation unchanged")
	r = run()
	r.wantOut(t, "sec_apple", "HOLD", "no rule fired")
	r.wantNotOut(t, "review required", "overdue")

	// a late review is overdue, never a fresh HOLD; recording a review moves the date
	late := func(extra ...string) result {
		pf := writeFile(t, e, "late.csv", candHeader+"sec_apple,,,technology,1.00 USD,2027-01-14,no,,,yes,\n")
		return e.run(t, append([]string{"review", "run", book, "-prices", pf, "-as-of", "2027-01-15", "-rates", "USD=0.92"}, extra...)...)
	}
	r = late()
	r.wantCode(t, exitOK)
	r.wantOut(t, "HOLD (review overdue by 45 days)")
	e.run(t, "review", "done", "-security", "sec_apple", "-outcome", "held", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "review", "done", "-security", "sec_apple", "-outcome", "maybe", "-note", "x", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "review", "done", "-security", "sec_apple", "-outcome", "held", "-note", "Reviewed the filing", "-by", "me", "-day", "2027-01-15").wantOut(t, "A review date is not a sale date")
	r = late()
	r.wantOut(t, "2027-04-16")
	r.wantNotOut(t, "overdue")
	e.run(t, "review", "thesis", "set", "-security", "sec_apple", "-status", "intact", "-note", "Still intact", "-source", "memo", "-ref", "memo 4", "-by", "me", "-day", "2027-01-14").wantCode(t, exitOK)

	// missing data never becomes HOLD
	nop := writeFile(t, e, "none.csv", candHeader+"sec_sap,,,technology,100.00 EUR,2026-11-02,no,,,yes,\n")
	r = e.run(t, "review", "run", book, "-prices", nop, "-as-of", "2026-11-03", "-rates", "USD=0.92")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ABSTAIN (blocked)", "missing: no current price")
	r = e.run(t, "review", "run", book, "-prices", prices, "-as-of", "2026-11-03")
	r.wantOut(t, "ABSTAIN (blocked)", "no USD->EUR rate")

	// your instruction decides first, bounded by the free shares
	e.run(t, "review", "instruct", "-security", "sec_apple", "-action", "reduce", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "review", "instruct", "-security", "sec_apple", "-action", "reduce", "-qty", "40", "-all", "-why", "withdrawal", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "review", "instruct", "-security", "sec_apple", "-action", "reduce", "-qty", "40", "-why", "withdrawal", "-by", "me", "-day", "2026-11-02").wantOut(t, "recording it sells nothing")
	e.run(t, "portfolio", "add", book, "reserve_shares", "-date", "2026-11-02", "-order", "o1", "-security", "sec_apple", "-qty", "70").wantCode(t, exitOK)
	r = run()
	r.wantCode(t, exitOK)
	r.wantOut(t, "REDUCE 30", "rule 1 withdrawal", "only 30 are free to sell", "proceeds: about")
	r.wantNotOut(t, "REDUCE 40")
	e.run(t, "portfolio", "add", book, "release", "-date", "2026-11-02", "-order", "o1").wantCode(t, exitOK)
	r = run()
	r.wantOut(t, "REDUCE 40")
	e.run(t, "review", "instruct", "-security", "sec_apple", "-clear", "-by", "me").wantOut(t, "withdrawn")
	run().wantOut(t, "no rule fired")

	// a broken thesis sells the free shares, and says what argues against it
	e.run(t, "review", "thesis", "set", "-security", "sec_apple", "-status", "broken", "-note", "Restatement of three years", "-source", "filing", "-ref", "8-K", "-by", "me", "-day", "2026-11-02").wantCode(t, exitOK)
	r = run()
	r.wantOut(t, "SELL 100", "rule 2 thesis_broken", "Restatement of three years")
	e.run(t, "review", "thesis", "set", "-security", "sec_apple", "-status", "intact", "-note", "Restated figures checked", "-source", "memo", "-ref", "5", "-by", "me", "-day", "2026-11-02").wantCode(t, exitOK)

	// thresholds nobody has read require a review but propose no sale
	cfg := e.run(t, "review", "config", "example")
	cfg.wantOut(t, "holdings-review-v1", `"reviewed": false`)
	e.run(t, "review", "config", "review").wantCode(t, exitError)
	wide := strings.Replace(cfg.out, `"goal_window_months": 12`, `"goal_window_months": 120`, 1)
	wide = strings.Replace(wide, `"derisk_fraction": "0"`, `"derisk_fraction": "0.25"`, 1)
	e.run(t, "review", "config", "set", "-file", writeFile(t, e, "rc.json", wide)).wantOut(t, "NOT reviewed")
	e.run(t, "review", "config", "set", "-file", writeFile(t, e, "bad.json", strings.Replace(cfg.out, `"review_days": 91`, `"review_days": 2`, 1))).wantCode(t, exitError)
	r = run()
	r.wantOut(t, "HOLD (review required)", "approaching_goal", "has not been reviewed by you")
	e.run(t, "review", "config", "review").wantOut(t, "marked as reviewed")
	r = run()
	r.wantOut(t, "REDUCE 25", "rule 3 approaching_goal", "where proceeds would go")
	e.run(t, "review", "config", "show").wantOut(t, `"reviewed": true`)

	// reviews are saved as plan revisions, which go stale when a decision is recorded
	r = run("-save")
	r.wantCode(t, exitOK)
	r.wantOut(t, "saved as plan revisions")
	pl := e.run(t, "plan", "list")
	pl.wantOut(t, "sec_apple", "REDUCE", "25")
	id := planIDRE.FindString(pl.out)
	e.run(t, "plan", "show", id).wantOut(t, "holdings-review-v1", "rule 3 (approaching_goal)", "reduce and sell rules", "1 your instruction")
	e.run(t, "plan", "check", id).wantOut(t, "unchanged")
	e.run(t, "review", "done", "-security", "sec_apple", "-outcome", "noted", "-note", "Looked at it", "-by", "me", "-day", "2026-11-03").wantCode(t, exitOK)
	e.run(t, "plan", "check", id).wantOut(t, "STALE", "recorded decisions")
	e.run(t, "plan", "propose", id, "-by", "me").wantCode(t, exitOK)
	r = e.run(t, "plan", "accept", id, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "stale", "recorded decisions changed")

	// concentration: the smallest reduction that cures the breach
	e.run(t, "portfolio", "add", book, "buy", "-date", "2026-11-02", "-security", "sec_apple", "-qty", "1900", "-gross", "1900.00 USD", "-import-id", "buy2").wantCode(t, exitOK)
	cfg2 := strings.Replace(cfg.out, `"goal_window_months": 12`, `"goal_window_months": 1`, 1)
	e.run(t, "review", "config", "set", "-file", writeFile(t, e, "rc2.json", cfg2)).wantCode(t, exitOK)
	e.run(t, "review", "config", "review").wantCode(t, exitOK)
	r = run()
	r.wantCode(t, exitOK)
	// 2,000 shares are 1,840.00 EUR of 5,600.00 EUR (the 3,000 USD of cash counts): 32.86%, 160.00 EUR over the 30% limit, 174 shares at 0.92
	r.wantOut(t, "REDUCE 174", "rule 3", "issuer iss_apple is 32.86% of equity, over the limit 30%", "smallest reduction that cures it is 174", "about 16008 minor units")
	r.wantNotOut(t, "cannot be cured")
	// shares reserved by an open sell order are not free: the part that cannot be cured is said
	e.run(t, "portfolio", "add", book, "reserve_shares", "-date", "2026-11-03", "-order", "o2", "-security", "sec_apple", "-qty", "1900").wantCode(t, exitOK)
	r = run()
	r.wantOut(t, "REDUCE 100", "only 100 are free", "74 of the breach cannot be cured now")
	// everything reserved: the sale is blocked, never invented
	e.run(t, "portfolio", "add", book, "reserve_shares", "-date", "2026-11-03", "-order", "o3", "-security", "sec_apple", "-qty", "100").wantCode(t, exitOK)
	r = run()
	r.wantOut(t, "HOLD (blocked)", "no shares are free to sell")
	// the program never sells: the book is untouched by any review
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)
}
