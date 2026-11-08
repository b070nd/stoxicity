package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var planIDRE = regexp.MustCompile(`pl_[0-9a-f]{12}`)

// planFixture builds the allocation environment with a funded EUR book and the
// candidate and fee files, with policy limits that let the stock and the fund fit.
func planFixture(t *testing.T) (*testEnv, string, string, string) {
	t.Helper()
	e := allocateEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	activatePolicy(t, e, func(s string) string {
		s = strings.Replace(s, "max_issuer: \"0.10\"", "max_issuer: \"0.30\"", 1)
		return strings.Replace(s, "max_sector: \"0.25\"", "max_sector: \"0.40\"", 1)
	})
	ff := e.run(t, "strategy", "fund", "-example").out
	ff = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(ff, "REPLACE: a broad world equity ETF", "World ETF"), "REPLACE: KID or factsheet", "KID"), "REPLACE: document and date", "KID 2026-01")
	ff = strings.ReplaceAll(ff, `"REPLACE"`, `"sec_exw_acc"`)
	ff = strings.ReplaceAll(ff, "2026-01-31", "2026-10-31")
	facts := writeFile(t, e, "fund.json", ff)
	cands := writeFile(t, e, "cands.csv", candHeader+
		"sec_apple,,,technology,1.00 USD,2026-11-02,no,,,yes,\n"+
		"sec_sap,,,technology,100.00 EUR,2026-11-02,no,,,yes,\n"+
		"sec_exw_acc,,,,50.00 EUR,2026-11-02,no,,,yes,"+facts+"\n")
	fees := writeFile(t, e, "fees.json", `{"known": true, "rate": "0.0005", "min": "1.25 EUR", "fixed": "0.00 EUR", "fx_rate": "0.002", "source": "my broker's schedule, 2026-02"}`)
	return e, cands, fees, "main/core"
}

func TestPlanSnapshotsAndLifecycle(t *testing.T) {
	e, cands, fees, book := planFixture(t)
	e.run(t, "plan").wantCode(t, exitUsage)
	e.run(t, "plan", "nope").wantCode(t, exitUsage)
	e.run(t, "plan", "list").wantOut(t, "no open plans")

	gen := func(extra ...string) result {
		args := append([]string{"plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92"}, extra...)
		return e.run(t, args...)
	}
	r := gen()
	r.wantCode(t, exitOK)
	r.wantOut(t, "sec_apple", "BUY", "new", "valid through 2026-11-06", "not an authorization")
	r.wantNotOut(t, "unchanged")
	var apple, fund, sap string
	for _, line := range strings.Split(r.out, "\n") {
		id := planIDRE.FindString(line)
		switch {
		case id == "":
		case strings.Contains(line, "sec_apple"):
			apple = id
		case strings.Contains(line, "sec_exw_acc"):
			fund = id
		case strings.Contains(line, "sec_sap"):
			sap = id
		}
	}
	if apple == "" || fund == "" || sap == "" {
		t.Fatalf("plan ids missing:\n%s", r.out)
	}

	// the same frozen inputs again: nothing new is stored
	r = gen()
	r.wantOut(t, "unchanged")
	r.wantNotOut(t, "new ", "revised")

	r = e.run(t, "plan", "list")
	r.wantOut(t, apple, fund, sap, "draft", "324", "2026-11-06")
	r = e.run(t, "plan", "show", apple)
	r.wantCode(t, exitOK)
	r.wantOut(t, apple+".r1", "BUY", "quantity 324 sized at 1.00 USD, entry at most 22.5985", "298.08 EUR", "issuer", "29.81%", "bear", "base", "bull", "spread: unavailable", "tax: unavailable",
		"what would change this", "reduce and sell rules", "a review date is not a sale date", "manifest", "policy epoch 1")
	r.wantNotOut(t, "INTEGRITY")
	e.run(t, "plan", "show", apple, "-json").wantOut(t, `"intact": true`, `"semantic"`, `"manifest_hash"`)
	e.run(t, "plan", "show", "pl_000000000000").wantCode(t, exitError)
	e.run(t, "plan", "show", apple+".rx").wantCode(t, exitUsage)

	// the lifecycle: draft cannot be accepted, a decision needs a name, a view has nothing to accept
	r = e.run(t, "plan", "accept", apple, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "is draft and cannot become accepted")
	e.run(t, "plan", "propose", apple).wantCode(t, exitError)
	e.run(t, "plan", "propose", apple, "-by", "me").wantOut(t, "draft -> proposed")
	e.run(t, "plan", "propose", fund, "-by", "me").wantCode(t, exitOK)
	e.run(t, "plan", "propose", sap, "-by", "me").wantCode(t, exitOK)
	r = e.run(t, "plan", "accept", sap, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "ABSTAIN plan records a view")
	e.run(t, "plan", "reject", sap, "-by", "me").wantCode(t, exitError)
	e.run(t, "plan", "reject", sap, "-by", "me", "-note", "no filings yet").wantOut(t, "proposed -> rejected")
	e.run(t, "plan", "accept", apple, "-by", "me", "-note", "reviewed").wantOut(t, "proposed -> accepted", "not an order and not an authorization")
	e.run(t, "plan", "show", apple).wantOut(t, "lifecycle:", "draft -> proposed by me", "proposed -> accepted by me")
	e.run(t, "plan", "check", apple).wantOut(t, "accepted", "unchanged")

	// something the plans were built on changes: they go stale but stay readable
	e.run(t, "portfolio", "add", book, "deposit", "-date", "2026-11-03", "-amount", "500.00 EUR").wantCode(t, exitOK)
	r = e.run(t, "plan", "check", fund)
	r.wantOut(t, "STALE", "book", "regenerate")
	r = e.run(t, "plan", "accept", fund, "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "stale", "book changed")
	e.run(t, "plan", "show", fund).wantNotOut(t, "INTEGRITY")

	// regenerating appends revisions: the open one is superseded, the accepted one stays in force
	r = e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-rates", "USD=0.92", "-as-of", "2026-11-03")
	r.wantCode(t, exitOK)
	r.wantOut(t, "revised (material)", "superseded "+fund+".r1", apple+".r1 stays accepted until you accept this")
	r = e.run(t, "plan", "diff", apple)
	r.wantCode(t, exitOK)
	r.wantOut(t, apple+": revision 1 -> 2", "quantity", "324", "inputs changed: book")
	e.run(t, "plan", "diff", sap).wantOut(t, "the cards are identical", "inputs changed: book")
	e.run(t, "plan", "diff", "pl_000000000000").wantCode(t, exitError)
	r = e.run(t, "plan", "show", apple+".r1")
	r.wantOut(t, "324", "accepted")
	r = e.run(t, "plan", "show", apple)
	r.wantOut(t, apple+".r2", "draft")
	e.run(t, "plan", "propose", apple, "-by", "me").wantCode(t, exitOK)
	e.run(t, "plan", "accept", apple, "-by", "me").wantOut(t, "proposed -> accepted")
	e.run(t, "plan", "show", apple+".r1").wantOut(t, "accepted -> superseded by system")

	// policy and evidence as of a day before the policy existed are not used
	r = e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-rates", "USD=0.92", "-as-of", "2026-11-01")
	r.wantCode(t, exitError)
	r.wantErr(t, "no investment policy is active")
	e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "never").wantCode(t, exitUsage)
	e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-valid-days", "0").wantCode(t, exitUsage)
	e.run(t, "plan", "generate").wantCode(t, exitUsage)

	// time passes: expiry is shown at once and recorded on request, never revived
	later := func(args ...string) result {
		return runWith(t, runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return testNow.AddDate(0, 0, 10) }}, append([]string{"-config-dir", e.dir}, args...)...)
	}
	// a note written after the plan was generated is something the plan did not see
	later("scorecard", "annotate", "-security", "sec_apple", "-topic", "thesis", "-stance", "counter", "-text", "Margins are under pressure", "-source", "memo", "-ref", "memo 9", "-at", "2026-11-09").wantCode(t, exitOK)
	later("plan", "check", apple).wantOut(t, "STALE", "scorecard")
	later("plan", "list").wantOut(t, "no open plans")
	r = later("plan", "list", "-all")
	r.wantOut(t, apple, "expired")
	later("plan", "show", apple).wantOut(t, "recorded as", "past its expiry day")
	r = later("plan", "propose", apple, "-by", "me")
	r.wantCode(t, exitError)
	later("plan", "expire").wantOut(t, "recorded 3 expired revision(s)")
	later("plan", "expire").wantOut(t, "recorded 0 expired revision(s)")
	later("plan", "show", apple).wantOut(t, "expired by system", "past its expiry day 2026-11-06")
}
