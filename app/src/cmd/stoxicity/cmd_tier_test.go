package main

import (
	"strings"
	"testing"
)

// capEdit is the edit the other tests make to the example policy.
func capEdit(s string) string {
	s = strings.Replace(s, "max_issuer: \"0.10\"", "max_issuer: \"0.30\"", 1)
	return strings.Replace(s, "max_sector: \"0.25\"", "max_sector: \"0.40\"", 1)
}

// tierUp goes through the whole door: sourced evidence, reviewed thresholds, the
// user's recorded acceptance, then a policy that carries the tier.
func tierUp(t *testing.T, e *testEnv, n int, edit func(string) string) {
	t.Helper()
	items := map[int][]string{1: {"config_complete", "records_reconciled"}}
	for _, it := range items[n] {
		e.run(t, "tier", "evidence", "set", "-target", "1", "-item", it, "-ref", "test run", "-by", "me").wantCode(t, exitOK)
	}
	e.run(t, "tier", "config", "review").wantCode(t, exitOK)
	e.run(t, "tier", "unlock", "1", "-by", "me", "-accept-risk").wantCode(t, exitOK)
	activatePolicy(t, e, func(s string) string {
		s = strings.Replace(s, "tier: 0", "tier: 1", 1)
		if edit != nil {
			s = edit(s)
		}
		return s
	})
}

func TestTierDoorNeedsEvidenceReviewAndAcceptance(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "tier").wantCode(t, exitUsage)
	e.run(t, "tier", "nope").wantCode(t, exitUsage)
	e.run(t, "tier", "status").wantOut(t, "tier 0: learn and build", "no active policy: paper only", "not suspended", "to reach tier 1")

	raise := func() result {
		file := writeReviewed(t, e, func(s string) string { return strings.Replace(s, "tier: 0", "tier: 1", 1) })
		p := e.run(t, "policy", "preview", "-file", file)
		p.wantCode(t, exitOK)
		m := hashLine.FindStringSubmatch(p.out)
		if m == nil {
			t.Fatal(p.out)
		}
		return e.run(t, "policy", "activate", "-file", file, "-confirm", m[1], "-accept-risk")
	}
	r := raise()
	r.wantCode(t, exitError)
	r.wantErr(t, "raising the tier to 1 needs readiness evidence", "config_complete: no evidence recorded")

	// evidence needs a source, an item of the right tier, and a name
	e.run(t, "tier", "evidence", "set", "-target", "1", "-item", "config_complete", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "tier", "evidence", "set", "-target", "1", "-item", "no_broker_discrepancy", "-ref", "x", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "tier", "evidence", "set", "-target", "1", "-item", "config_complete", "-ref", "setup checklist 2026-11-07", "-by", "me").wantOut(t, "config_complete met", "setup checklist")
	e.run(t, "tier", "evidence", "set", "-target", "1", "-item", "records_reconciled", "-ref", "reconcile run", "-met", "no", "-by", "me", "-note", "one open exception").wantCode(t, exitOK)
	r = e.run(t, "tier", "ready", "1")
	r.wantOut(t, "NOT READY", "records_reconciled: recorded as not met", "illustrations you have not reviewed", "setup checklist")
	e.run(t, "tier", "unlock", "1", "-by", "me", "-accept-risk").wantCode(t, exitError)

	e.run(t, "tier", "evidence", "set", "-target", "1", "-item", "records_reconciled", "-ref", "reconcile run 2", "-by", "me").wantCode(t, exitOK)
	e.run(t, "tier", "config", "review").wantOut(t, "reviewed")
	e.run(t, "tier", "ready", "1").wantOut(t, "READY", "Profit, a winning streak and time passing do not unlock a tier")
	e.run(t, "tier", "evidence", "show").wantOut(t, "reconcile run", "reconcile run 2", "one open exception")

	// accepting is a deliberate act
	u := e.run(t, "tier", "unlock", "1", "-by", "me")
	u.wantCode(t, exitUsage)
	u.wantOut(t, "I accept tier 1", "nothing here is advice")
	// readiness alone does not raise the tier: the policy has to carry it
	raise().wantCode(t, exitError)
	e.run(t, "tier", "unlock", "1", "-by", "me", "-accept-risk").wantOut(t, "Nothing has changed yet")

	// new evidence after the decision makes the decision stale
	e.run(t, "tier", "evidence", "set", "-target", "1", "-item", "config_complete", "-ref", "setup checklist 2026-11-08", "-by", "me").wantCode(t, exitOK)
	r = raise()
	r.wantCode(t, exitError)
	r.wantErr(t, "evidence changed after the decision")
	e.run(t, "tier", "unlock", "1", "-by", "me", "-accept-risk").wantCode(t, exitOK)
	r = raise()
	r.wantCode(t, exitOK)
	e.run(t, "tier", "status").wantOut(t, "tier 1: conservative ownership", "the active policy", "a real portfolio managed by hand", "the same at every tier")
	e.run(t, "tier", "status", "-json").wantOut(t, `"tier": 1`, `"manual_real": true`, `"broker_submission": false`)
}

func TestElapsedTimeAndProfitNeverUnlockATier(t *testing.T) {
	e := newTestEnv(t)
	tierUp(t, e, 1, nil)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-06-01", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	// a long, profitable history: months pass and the account grows
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-10-30", "-amount", "5000.00 EUR").wantCode(t, exitOK)
	r := e.run(t, "tier", "ready", "2")
	r.wantOut(t, "NOT READY", "no_broker_discrepancy: no evidence recorded", "failure_scenarios: no evidence recorded", "evaluation_adequate: no evidence recorded")
	r.wantOut(t, "completed sessions")
	for _, it := range []string{"config_complete", "records_reconciled", "no_broker_discrepancy", "failure_scenarios", "evaluation_adequate"} {
		e.run(t, "tier", "evidence", "set", "-target", "2", "-item", it, "-ref", "report", "-by", "me").wantCode(t, exitOK)
	}
	// every item is there and the sessions are enough: ready, and only then
	e.run(t, "tier", "ready", "2").wantOut(t, "READY")
	// ... but with the observation floor raised above the sessions observed, it is not
	cfg := e.run(t, "tier", "config", "example").out
	high := strings.Replace(cfg, `"2": 30`, `"2": 500`, 1)
	e.run(t, "tier", "config", "set", "-file", writeFile(t, e, "tc.json", high)).wantOut(t, "NOT reviewed")
	r = e.run(t, "tier", "ready", "2")
	r.wantOut(t, "NOT READY", "illustrations you have not reviewed", "of 500")
	e.run(t, "tier", "config", "set", "-file", writeFile(t, e, "bad.json", strings.Replace(cfg, `"decision_valid_days": 7`, `"decision_valid_days": 0`, 1))).wantCode(t, exitError)
	e.run(t, "tier", "config", "show").wantOut(t, `"reviewed": false`)
	e.run(t, "tier", "ready", "4").wantCode(t, exitUsage)
	e.run(t, "tier", "ready", "x").wantCode(t, exitUsage)
}

func TestHealthSignalsSuspendNewRiskAndTouchNothingElse(t *testing.T) {
	e := allocateEnv(t)
	tierUp(t, e, 1, func(s string) string {
		return strings.Replace(capEdit(s), "max_trades_per_month: 2", "max_trades_per_month: 20", 1)
	})
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-10-30", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-10-31", "-security", "sec_apple", "-qty", "10", "-gross", "100.00 EUR", "-import-id", "b1").wantCode(t, exitOK)
	warn := "no executing quote has been validated: the price collar and quote freshness are not checked here"
	draft := func(side, qty, limit string) result {
		return e.run(t, "order", "draft", "main/core", "-security", "sec_apple", "-side", side, "-qty", qty, "-limit", limit, "-by", "me")
	}
	e.run(t, "tier", "signal", "show").wantOut(t, "no health signal")

	buy := orderIDRE.FindString(draft("buy", "5", "20").out)
	sell := orderIDRE.FindString(draft("sell", "4", "30").out)
	e.run(t, "order", "confirm", buy, "-by", "me", "-ack", warn).wantCode(t, exitOK)
	e.run(t, "order", "confirm", sell, "-by", "me", "-ack", warn).wantCode(t, exitOK)

	e.run(t, "tier", "signal", "set", "nonsense", "-reason", "x", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "tier", "signal", "set", "stale_execution_inputs", "-by", "me").wantCode(t, exitUsage)
	r := e.run(t, "tier", "signal", "set", "stale_execution_inputs", "-reason", "quotes are three days old", "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "new risk is suspended", "1 unsent confirmation(s) revoked", "selling, cancelling and reporting stay possible")
	e.run(t, "tier", "status").wantOut(t, "NEW RISK IS SUSPENDED", "stale_execution_inputs: quotes are three days old", "Holdings and open orders are untouched")

	// the buy is a draft again and cannot be confirmed or drafted anew; the sale is untouched
	e.run(t, "order", "show", buy).wantOut(t, "draft", "confirmation_revoked", "stale_execution_inputs")
	c := e.run(t, "order", "confirm", buy, "-by", "me", "-ack", warn)
	c.wantCode(t, exitError)
	c.wantErr(t, "new risk is suspended", "stale_execution_inputs")
	draft("buy", "1", "21").wantCode(t, exitError)
	e.run(t, "order", "show", sell).wantOut(t, "confirmed")
	// a sale can still be drafted while new risk is suspended, and cancelling is always possible
	draft("sell", "1", "31").wantCode(t, exitOK)
	e.run(t, "order", "cancel", buy, "-by", "me").wantCode(t, exitOK)
	e.run(t, "portfolio", "show", "main/core").wantOut(t, "sec_apple", "10")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)

	// resuming is an explicit act
	e.run(t, "tier", "signal", "clear", "stale_execution_inputs", "-reason", "fresh quotes checked", "-by", "me").wantOut(t, "cleared", "stay revoked")
	e.run(t, "tier", "status").wantOut(t, "new risk is not suspended")
	draft("buy", "1", "21").wantCode(t, exitOK)
	e.run(t, "tier", "signal", "show").wantOut(t, "stale_execution_inputs", "cleared")
}

func TestDrawdownCheckSuspendsAndOnlySuspends(t *testing.T) {
	e := allocateEnv(t)
	tierUp(t, e, 1, capEdit)
	e.run(t, "tier", "check").wantCode(t, exitUsage)
	// the policy threshold is 0.15
	ok := writeFile(t, e, "ok.csv", "day,equity\n2026-10-01,1000.00\n2026-10-02,900.00\n2026-10-03,950.00\n")
	e.run(t, "tier", "check", "-equity", ok).wantOut(t, "10.00%", "no suspension")
	e.run(t, "tier", "status").wantOut(t, "not suspended")
	bad := writeFile(t, e, "bad.csv", "day,equity\n2026-10-03,1100.00\n2026-10-01,1000.00\n2026-10-02,1300.00\n2026-10-04,1100.00\n")
	r := e.run(t, "tier", "check", "-equity", bad)
	r.wantCode(t, exitOK)
	r.wantOut(t, "15.38%", "NEW RISK IS SUSPENDED", "policy threshold 0.15")
	e.run(t, "tier", "status").wantOut(t, "drawdown:")
	// a better series does not resume anything by itself
	e.run(t, "tier", "check", "-equity", ok).wantOut(t, "no suspension")
	e.run(t, "tier", "status").wantOut(t, "NEW RISK IS SUSPENDED")
	e.run(t, "tier", "signal", "clear", "drawdown", "-reason", "reviewed with my notes", "-by", "me").wantCode(t, exitOK)
	e.run(t, "tier", "status").wantOut(t, "not suspended")
	// input mistakes are refused
	e.run(t, "tier", "check", "-equity", writeFile(t, e, "dup.csv", "day,equity\n2026-10-01,1.00\n2026-10-01,2.00\n")).wantCode(t, exitError)
	e.run(t, "tier", "check", "-equity", writeFile(t, e, "d.csv", "day,equity\nyesterday,1.00\n")).wantCode(t, exitError)
	e.run(t, "tier", "check", "-equity", writeFile(t, e, "c.csv", "day,equity\n2026-10-01,1.005\n")).wantCode(t, exitError)
}

func TestLoweringTheTierRevokesUnsentBuysAndPaperIsNeverGated(t *testing.T) {
	e := allocateEnv(t)
	tierUp(t, e, 1, capEdit)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-10-30", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "paper", "-name", "sim", "-env", "paper_local", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "paper/sim", "deposit", "-date", "2026-10-30", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	warn := "no executing quote has been validated: the price collar and quote freshness are not checked here"
	id := orderIDRE.FindString(e.run(t, "order", "draft", "main/core", "-security", "sec_apple", "-side", "buy", "-qty", "2", "-limit", "10", "-by", "me").out)
	e.run(t, "order", "confirm", id, "-by", "me", "-ack", warn).wantCode(t, exitOK)
	e.run(t, "tier", "signal", "set", "manual", "-reason", "away for a month", "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "2", "-limit", "10", "-by", "me").wantCode(t, exitOK)
	e.run(t, "tier", "signal", "clear", "manual", "-reason", "back", "-by", "me").wantCode(t, exitOK)

	e.run(t, "order", "confirm", id, "-by", "me", "-ack", warn).wantCode(t, exitOK)
	// back to tier 0: the policy that carries it is activated like any other
	file := writeReviewed(t, e, capEdit)
	p := e.run(t, "policy", "preview", "-file", file)
	m := hashLine.FindStringSubmatch(p.out)
	r := e.run(t, "policy", "activate", "-file", file, "-confirm", m[1])
	r.wantCode(t, exitOK)
	r.wantOut(t, "tier lowered from 1 to 0", "1 unsent confirmation(s) revoked", "holdings and open orders are untouched")
	e.run(t, "order", "show", id).wantOut(t, "confirmation_revoked", "lowered")
	// tier 0 is paper only: real drafts are refused, paper ones are not
	d := e.run(t, "order", "draft", "main/core", "-security", "sec_apple", "-side", "buy", "-qty", "1", "-limit", "10", "-by", "me")
	d.wantCode(t, exitError)
	d.wantErr(t, "tier 0 allows paper trading only")
	e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "1", "-limit", "11", "-by", "me").wantCode(t, exitOK)
}
