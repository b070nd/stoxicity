package main

import "testing"

const scHeader = "security,metric,start,end,currency,value,source,ref,filed,note\n"

func TestScorecardWorkflow(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)

	r := e.run(t, "scorecard", "example")
	r.wantOut(t, "security,metric", "invented illustration")
	r = e.run(t, "scorecard", "show", "-security", "sec_apple")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no annual figures")
	e.run(t, "scorecard").wantCode(t, exitUsage)
	e.run(t, "scorecard", "nope").wantCode(t, exitUsage)

	body := scHeader
	for _, y := range []struct{ y, rev, ni, eq, filed string }{
		{"2023", "1000", "100", "500", "2024-03-01"}, {"2024", "1100", "121", "550", "2025-03-01"}, {"2025", "1210", "133", "600", "2026-03-01"},
	} {
		body += "sec_apple,revenue," + y.y + "-01-01," + y.y + "-12-31,USD," + y.rev + ",annual report,AR " + y.y + " p.40," + y.filed + ",\n"
		body += "sec_apple,net_income," + y.y + "-01-01," + y.y + "-12-31,USD," + y.ni + ",annual report,AR " + y.y + " p.40," + y.filed + ",\n"
		body += "sec_apple,equity,," + y.y + "-12-31,USD," + y.eq + ",annual report,AR " + y.y + " p.44," + y.filed + ",\n"
	}
	f := writeFile(t, e, "sc.csv", body)
	r = e.run(t, "scorecard", "import", "-file", f)
	r.wantCode(t, exitOK)
	r.wantOut(t, "9 figures read, 9 new")
	r = e.run(t, "scorecard", "import", "-file", f)
	r.wantOut(t, "0 new, 9 already stored")

	// a security that is not registered, and a figure without a source, are refused whole
	bad := writeFile(t, e, "bad.csv", scHeader+"sec_nope,revenue,2025-01-01,2025-12-31,USD,1,x,y,,\n")
	r = e.run(t, "scorecard", "import", "-file", bad)
	r.wantCode(t, exitError)
	nosrc := writeFile(t, e, "nosrc.csv", scHeader+"sec_apple,revenue,2025-01-01,2025-12-31,USD,1,,,,\n")
	r = e.run(t, "scorecard", "import", "-file", nosrc)
	r.wantCode(t, exitError)
	r.wantErr(t, "nothing was imported")

	// without a template nothing is computed
	r = e.run(t, "scorecard", "show", "-security", "sec_apple", "-reasons")
	r.wantCode(t, exitOK)
	r.wantOut(t, "read as a unknown", "2025-12-31", "unsupported", "no template has been chosen")
	e.run(t, "scorecard", "template", "-security", "sec_apple", "-template", "spaceship").wantCode(t, exitUsage)
	r = e.run(t, "scorecard", "template", "-list")
	r.wantOut(t, "bank", "reit", "FFO"[:0]+"none")
	e.run(t, "scorecard", "template", "-security", "sec_apple", "-template", "ordinary").wantCode(t, exitOK)
	r = e.run(t, "scorecard", "show", "-security", "sec_apple", "-reasons")
	r.wantOut(t, "read as a ordinary", "0.1099", "0.2217", "0.1000", "no_prior_year", "missing from the newest year", "operating_cash_flow")

	// a bank gets no ordinary ratios
	e.run(t, "scorecard", "template", "-security", "sec_apple", "-template", "bank").wantCode(t, exitOK)
	r = e.run(t, "scorecard", "show", "-security", "sec_apple", "-reasons")
	r.wantOut(t, "a bank's revenue, debt and cash flow mean something else")
	r.wantNotOut(t, "0.1099")

	// as of a day before the last filing, the newest year is not there
	r = e.run(t, "scorecard", "show", "-security", "sec_apple", "-as-of", "2025-06-01", "-json")
	r.wantCode(t, exitOK)
	r.wantNotOut(t, "2025-12-31")

	// notes: validated, flagged, idempotent, in the card
	r = e.run(t, "scorecard", "annotate", "-security", "sec_apple", "-topic", "risk", "-stance", "counter", "-text", "Class action over the restatement", "-source", "analyst", "-ref", "memo 3")
	r.wantCode(t, exitOK)
	r.wantOut(t, "litigation, restatement")
	r = e.run(t, "scorecard", "annotate", "-security", "sec_apple", "-topic", "risk", "-stance", "counter", "-text", "Class action over the restatement", "-source", "analyst", "-ref", "memo 3")
	r.wantOut(t, "already recorded")
	e.run(t, "scorecard", "annotate", "-security", "sec_apple", "-topic", "risk", "-text", "no source").wantCode(t, exitUsage)
	r = e.run(t, "scorecard", "show", "-security", "sec_apple")
	r.wantOut(t, "risk/counter", "litigation, restatement", "memo 3")
	r = e.run(t, "scorecard", "versions")
	r.wantOut(t, "scorecard-extract/1", "scorecard-rules/1:", "scorecard-keywords/1:")
	e.run(t, "scorecard", "show").wantCode(t, exitUsage)
}
