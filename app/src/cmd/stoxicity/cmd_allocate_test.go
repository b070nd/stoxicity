package main

import (
	"strings"
	"testing"
)

// allocateEnv sets up a reviewed strategy for sec_apple that says BUY at 1.00 USD,
// an active policy that allows 30% in one issuer, and an EUR book with 1,000.00 EUR.
func allocateEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "scorecard", "import", "-file", writeFile(t, e, "f.csv", strategyFigures("sec_apple"))).wantCode(t, exitOK)
	e.run(t, "scorecard", "template", "-security", "sec_apple", "-template", "ordinary").wantCode(t, exitOK)
	for _, topic := range []string{"thesis", "moat", "management"} {
		e.run(t, "scorecard", "annotate", "-security", "sec_apple", "-topic", topic, "-stance", "supports", "-text", "A sourced "+topic+" note", "-source", "memo", "-ref", "memo 1", "-at", "2026-01-10").wantCode(t, exitOK)
	}
	as := e.run(t, "strategy", "assumptions", "example")
	good := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(as.out, `"currency": "EUR"`, `"currency": "USD"`), "REPLACE: who made these assumptions", "my memo"), "REPLACE: the memo or model", "memo 2026-02")
	e.run(t, "strategy", "assumptions", "set", "-security", "sec_apple", "-file", writeFile(t, e, "as.json", good)).wantCode(t, exitOK)
	cfg := e.run(t, "strategy", "config", "example")
	e.run(t, "strategy", "config", "set", "-file", writeFile(t, e, "c.json", cfg.out)).wantCode(t, exitOK)
	e.run(t, "strategy", "config", "review").wantCode(t, exitOK)
	return e
}

func activatePolicy(t *testing.T, e *testEnv, edit func(string) string) {
	t.Helper()
	file := writeReviewed(t, e, edit)
	r := e.run(t, "policy", "preview", "-file", file)
	r.wantCode(t, exitOK)
	m := hashLine.FindStringSubmatch(r.out)
	if m == nil {
		t.Fatalf("no hash in the preview:\n%s", r.out)
	}
	args := []string{"policy", "activate", "-file", file, "-confirm", m[1]}
	if strings.Contains(r.out, "-accept-risk") {
		args = append(args, "-accept-risk")
	}
	e.run(t, args...).wantCode(t, exitOK)
}

const candHeader = "security,mic,ticker,sector,price,price_day,fractional,step,min_qty,tradable,facts\n"

func TestAllocatePlan(t *testing.T) {
	e := allocateEnv(t)
	e.run(t, "allocate").wantCode(t, exitUsage)
	e.run(t, "allocate", "example").wantOut(t, "security,mic,ticker", "fees.json")

	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	cands := writeFile(t, e, "cands.csv", candHeader+
		"sec_apple,,,technology,1.00 USD,2026-02-27,no,,,yes,\n"+
		"sec_sap,,,technology,100.00 EUR,2026-02-27,no,,,yes,\n"+
		"sec_exw_acc,,,,50.00 EUR,2026-02-27,no,,,yes,\n")
	fees := writeFile(t, e, "fees.json", `{"known": true, "rate": "0.0005", "min": "1.25 EUR", "fixed": "0.00 EUR", "fx_rate": "0.002", "source": "my broker's schedule, 2026-02"}`)

	// without an active policy the limits are unknown, so nothing is proposed
	r := e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-fees", fees, "-as-of", "2026-03-01", "-rates", "USD=0.92")
	r.wantCode(t, exitError)
	r.wantErr(t, "no investment policy is active")

	activatePolicy(t, e, func(s string) string {
		s = strings.Replace(s, "max_issuer: \"0.10\"", "max_issuer: \"0.30\"", 1)
		return strings.Replace(s, "max_sector: \"0.25\"", "max_sector: \"0.40\"", 1)
	})

	r = e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-fees", fees, "-as-of", "2026-03-01", "-rates", "USD=0.92")
	r.wantCode(t, exitOK)
	r.wantOut(t, "FEASIBLE", "sec_apple", "324", "298.08 EUR", "1.85 EUR", "sized by the issuer limit", "700.07 EUR", "ABSTAIN", "documented facts", "no sale is proposed", "not an order")

	// a fund with documented facts joins as the core candidate
	ff := e.run(t, "strategy", "fund", "-example").out
	ff = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(ff, "REPLACE: a broad world equity ETF", "World ETF"), "REPLACE: KID or factsheet", "KID"), "REPLACE: document and date", "KID 2026-01")
	ff = strings.ReplaceAll(ff, `"REPLACE"`, `"sec_exw_acc"`)
	facts := writeFile(t, e, "fund.json", ff)
	cands2 := writeFile(t, e, "cands2.csv", candHeader+
		"sec_apple,,,technology,1.00 USD,2026-02-27,no,,,yes,\n"+
		"sec_exw_acc,,,,50.00 EUR,2026-02-27,no,,,yes,"+facts+"\n")
	r = e.run(t, "allocate", "plan", "main/core", "-candidates", cands2, "-fees", fees, "-as-of", "2026-03-01", "-rates", "USD=0.92")
	r.wantCode(t, exitOK)
	r.wantOut(t, "sec_exw_acc", "250.00 EUR", "548.08 EUR", "448.82 EUR")

	// the options side by side, unranked
	r = e.run(t, "allocate", "plan", "main/core", "-candidates", cands2, "-fees", fees, "-as-of", "2026-03-01", "-rates", "USD=0.92", "-compare")
	r.wantCode(t, exitOK)
	r.wantOut(t, "diversified core fund", "individual stocks", "cash and contributions", "the options are not ranked", "nothing here is a forecast")

	// unsourced or missing fees are not guessed
	nofee := writeFile(t, e, "nofee.json", `{"known": true, "rate": "0.0005"}`)
	e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-fees", nofee, "-as-of", "2026-03-01", "-rates", "USD=0.92").wantErr(t, "needs its source")
	r = e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-as-of", "2026-03-01", "-rates", "USD=0.92")
	r.wantOut(t, "WAIT", "trading costs are unknown", "no claim is made that any purchase is affordable")
	// a USD price without a rate cannot be sized
	r = e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-fees", fees, "-as-of", "2026-03-01")
	r.wantOut(t, "NO FEASIBLE PURCHASE", "no USD->EUR rate was supplied")
	// too dear for the strategy
	dear := writeFile(t, e, "dear.csv", candHeader+"sec_apple,,,technology,1000.00 USD,2026-02-27,no,,,yes,\n")
	r = e.run(t, "allocate", "plan", "main/core", "-candidates", dear, "-fees", fees, "-as-of", "2026-03-01", "-rates", "USD=0.92")
	r.wantOut(t, "NO FEASIBLE PURCHASE", "the strategy says WATCH", "above the ceiling")
	r = e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-fees", fees, "-as-of", "2026-03-01", "-rates", "USD=0.92", "-json")
	r.wantOut(t, `"feasible": true`)

	// malformed input
	e.run(t, "allocate", "plan").wantCode(t, exitUsage)
	e.run(t, "allocate", "plan", "main/core", "-candidates", writeFile(t, e, "x.csv", candHeader+"sec_nope,,,,1.00 EUR,2026-02-27,,,,,\n"), "-fees", fees).wantCode(t, exitError)
	e.run(t, "allocate", "plan", "main/core", "-candidates", writeFile(t, e, "y.csv", candHeader+"sec_apple,,,,1.00 USD,,,,,,\n"), "-fees", fees).wantCode(t, exitError)
	e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-fees", fees, "-rates", "USD").wantCode(t, exitUsage)
	e.run(t, "allocate", "plan", "main/core", "-candidates", cands, "-fees", fees, "-as-of", "never").wantCode(t, exitUsage)
	e.run(t, "allocate", "nope").wantCode(t, exitUsage)
}
