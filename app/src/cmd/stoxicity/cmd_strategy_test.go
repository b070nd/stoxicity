package main

import (
	"strings"
	"testing"
)

func strategyFigures(sec string) string {
	body := scHeader
	rows := []struct{ y, rev, ni, ocf, capex, eq, oi, ie, filed string }{
		{"2023", "1000", "100", "200", "50", "600", "200", "20", "2024-03-01"},
		{"2024", "1100", "121", "220", "50", "600", "200", "20", "2025-03-01"},
		{"2025", "1210", "133", "240", "60", "600", "200", "20", "2026-03-01"},
	}
	for _, r := range rows {
		add := func(metric, v string, flow bool) {
			start := ""
			if flow {
				start = r.y + "-01-01"
			}
			body += sec + "," + metric + "," + start + "," + r.y + "-12-31,USD," + v + ",annual report,AR " + r.y + "," + r.filed + ",\n"
		}
		add("revenue", r.rev, true)
		add("net_income", r.ni, true)
		add("operating_cash_flow", r.ocf, true)
		add("capex", r.capex, true)
		add("equity", r.eq, false)
		add("operating_income", r.oi, true)
		add("interest_expense", r.ie, true)
	}
	body += sec + ",ebitda,2025-01-01,2025-12-31,USD,290,annual report,AR 2025,2026-03-01,\n"
	body += sec + ",total_debt,,2025-12-31,USD,400,annual report,AR 2025,2026-03-01,\n"
	body += sec + ",cash,,2025-12-31,USD,100,annual report,AR 2025,2026-03-01,\n"
	body += sec + ",shares,,2025-12-31,,100,annual report,AR 2025,2026-03-01,\n"
	return body
}

func TestStrategyWorkflow(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "scorecard", "import", "-file", writeFile(t, e, "f.csv", strategyFigures("sec_apple"))).wantCode(t, exitOK)
	e.run(t, "scorecard", "template", "-security", "sec_apple", "-template", "ordinary").wantCode(t, exitOK)
	for _, topic := range []string{"thesis", "moat", "management"} {
		e.run(t, "scorecard", "annotate", "-security", "sec_apple", "-topic", topic, "-stance", "supports", "-text", "A sourced "+topic+" note", "-source", "memo", "-ref", "memo 1", "-at", "2026-01-10").wantCode(t, exitOK)
	}
	e.run(t, "strategy").wantCode(t, exitUsage)

	// with no assumptions a company that pays no dividend cannot reach the minimum score
	r := e.run(t, "strategy", "propose", "-security", "sec_apple", "-as-of", "2026-03-01")
	r.wantCode(t, exitOK)
	r.wantOut(t, "using the starter configuration", "ABSTAIN", "no valuation assumptions were supplied", "score 65 of 100 is below the minimum 70", "a policy ordering, not a probability", "execution research_only")

	// the example assumptions carry placeholders that must be replaced
	r = e.run(t, "strategy", "assumptions", "example")
	r.wantOut(t, "owner_earnings_dcf", "REPLACE")
	ex := writeFile(t, e, "as.json", r.out)
	rr := e.run(t, "strategy", "assumptions", "set", "-security", "sec_apple", "-file", ex)
	rr.wantCode(t, exitError)
	rr.wantErr(t, "placeholder")
	good := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(r.out, `"currency": "EUR"`, `"currency": "USD"`), "REPLACE: who made these assumptions", "my memo"), "REPLACE: the memo or model", "memo 2026-02")
	e.run(t, "strategy", "assumptions", "set", "-security", "sec_apple", "-file", writeFile(t, e, "as2.json", good)).wantCode(t, exitOK)
	e.run(t, "strategy", "assumptions", "show", "-security", "sec_apple").wantOut(t, "my memo")
	e.run(t, "strategy", "assumptions", "show", "-security", "sec_sap").wantOut(t, "can be researched but not priced")
	bad := strings.ReplaceAll(good, `"discount": "0.09"`, `"discount": "0.01"`)
	e.run(t, "strategy", "assumptions", "set", "-security", "sec_apple", "-file", writeFile(t, e, "as3.json", bad)).wantCode(t, exitError)

	// priced, but the starter configuration is unreviewed: nothing stronger than WATCH
	r = e.run(t, "strategy", "propose", "-security", "sec_apple", "-as-of", "2026-03-01", "-price", "1.00 USD", "-price-day", "2026-02-27")
	r.wantCode(t, exitOK)
	r.wantOut(t, "WATCH", "has not been reviewed", "entry ceiling", "bear", "base", "bull", "scenarios are assumptions laid out", "val.entry")
	// a missing price is WAIT
	r = e.run(t, "strategy", "propose", "-security", "sec_apple", "-as-of", "2026-03-01")
	r.wantOut(t, "WAIT")

	// reviewing needs a stored configuration first
	e.run(t, "strategy", "config", "review").wantCode(t, exitError)
	cfg := e.run(t, "strategy", "config", "example")
	cfg.wantOut(t, `"reviewed": false`, "quality-income-core-v1")
	e.run(t, "strategy", "config", "set", "-file", writeFile(t, e, "c.json", cfg.out)).wantOut(t, "NOT reviewed")
	e.run(t, "strategy", "config", "review").wantOut(t, "reviewed by you", "not evidence of any edge")
	e.run(t, "strategy", "config", "show").wantOut(t, `"reviewed": true`)
	r = e.run(t, "strategy", "propose", "-security", "sec_apple", "-as-of", "2026-03-01", "-price", "1.00 USD", "-price-day", "2026-02-27")
	r.wantOut(t, "BUY", "inside the entry band"[:0]+"score")
	r.wantNotOut(t, "using the starter configuration")
	r = e.run(t, "strategy", "propose", "-security", "sec_apple", "-as-of", "2026-03-01", "-price", "1000.00 USD", "-price-day", "2026-02-27", "-json")
	r.wantCode(t, exitOK)
	r.wantOut(t, `"action": "WATCH"`, "outside the entry band", `"execution_eligibility": "research_only"`)
	e.run(t, "strategy", "propose", "-security", "sec_apple", "-price", "1.00 USD").wantCode(t, exitUsage)
	e.run(t, "strategy", "propose", "-security", "sec_apple", "-as-of", "never").wantCode(t, exitUsage)
	e.run(t, "strategy", "propose").wantCode(t, exitUsage)

	// a security with no figures abstains with the reason
	r = e.run(t, "strategy", "propose", "-security", "sec_sap", "-as-of", "2026-03-01")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ABSTAIN", "unknown, required", "insufficient")

	// funds are checked, not scored
	r = e.run(t, "strategy", "fund", "-example")
	r.wantOut(t, "REPLACE")
	e.run(t, "strategy", "fund", "-file", writeFile(t, e, "fund.json", r.out)).wantCode(t, exitError)
	fund := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(r.out, "REPLACE: a broad world equity ETF", "World ETF"), "REPLACE: KID or factsheet", "KID"), "REPLACE: document and date", "KID 2026-01")
	fund = strings.ReplaceAll(fund, `"REPLACE"`, `"fnd_world"`)
	r = e.run(t, "strategy", "fund", "-file", writeFile(t, e, "fund2.json", fund), "-as-of", "2026-03-01")
	r.wantCode(t, exitOK)
	r.wantOut(t, "eligible as a broad core fund", "never compared with a company by number")
}
