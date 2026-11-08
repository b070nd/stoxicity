package main

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// TestAcceptanceVerticalPath is the one smoke path the plan asks for before
// anything is broadened: an account is opened, a sourced distribution is
// imported, an order is drafted, confirmed and submitted on a local paper book,
// a conservative simulated fill arrives from finished bars, the income the
// filled shares earned is accrued and received once, and the book reconciles to
// a statement. It uses fixtures only: no network, no broker, no charge.
func TestAcceptanceVerticalPath(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "paper", "-name", "sim", "-env", "paper_local", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "paper/sim", "deposit", "-date", "2026-10-01", "-amount", "10000.00 USD").wantCode(t, exitOK)

	r := e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "10", "-limit", "50", "-fee", "2.00 USD", "-by", "me")
	r.wantCode(t, exitOK)
	id := orderIDRE.FindString(r.out)
	if id == "" {
		t.Fatalf("no order id:\n%s", r.out)
	}
	e.run(t, "order", "confirm", id, "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitOK)

	later := func(args ...string) result {
		return runWith(t, runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return testNow.AddDate(0, 0, 10) }}, append([]string{"-config-dir", e.dir}, args...)...)
	}
	bars := writeFile(t, e, "bars.csv", paperBars)
	later("paper", "run", "paper/sim", "-bars", bars, "-seed", "7").wantCode(t, exitOK)
	e.run(t, "order", "show", id).wantOut(t, "filled")

	// a distribution sourced from a file, with an ex-day after the fill
	div := writeFile(t, e, "div.csv", divHeader+"ordinary,2026Q4,declared,0.25,USD,2026-11-10,2026-11-10,2026-11-13,XNAS,AAPL\n")
	e.run(t, "dividends", "import", "-file", div, "-source", "company").wantCode(t, exitOK)
	r = later("income", "accrue", "paper/sim", "-as-of", "2026-11-11")
	r.wantCode(t, exitOK)
	r.wantOut(t, "accrued sec_apple|ordinary|2026Q4 2.50 USD")
	later("income", "accrue", "paper/sim", "-as-of", "2026-11-11").wantOut(t, "already booked")
	later("income", "receive", "paper/sim", "-payout", "sec_apple|ordinary|2026Q4", "-date", "2026-11-13", "-import-id", "pay1").wantCode(t, exitOK)
	later("income", "check", "paper/sim", "-as-of", "2026-11-20").wantCode(t, exitOK)

	// cash: 10000 - 10 x 50 - 2 fee + 2.50 income
	stmt := writeFile(t, e, "stmt.csv", "kind,security,qty,currency,amount\ncash,,,USD,9500.50\nposition,sec_apple,10,,\n")
	// the statement counts the purchase as unsettled until the settlement day is booked
	r = later("portfolio", "reconcile", "paper/sim", "-file", stmt, "-as-of", "2026-11-13")
	r.wantCode(t, exitError)
	r.wantOut(t, "equals the trades the ledger holds as unsettled", "Nothing was changed")
	var buyTx string
	for _, line := range strings.Split(later("portfolio", "journal", "paper/sim").out, "\n") {
		if strings.Contains(line, " buy ") {
			buyTx = regexp.MustCompile(`tx_[0-9a-f]{16}`).FindString(line)
		}
	}
	if buyTx == "" {
		t.Fatal("no buy in the journal")
	}
	later("portfolio", "add", "paper/sim", "settle", "-date", "2026-11-06", "-ref", buyTx).wantCode(t, exitOK)
	r = later("portfolio", "reconcile", "paper/sim", "-file", stmt, "-as-of", "2026-11-13")
	r.wantCode(t, exitOK)
	r.wantOut(t, "reconciled")
	later("portfolio", "verify").wantCode(t, exitOK)
}

type countingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.urls = append(c.urls, r.URL.String())
	c.mu.Unlock()
	return nil, errors.New("the test forbids network access")
}

// TestAcceptanceEmptyEURPortfolioMakesNoRequest is scenario 1: an empty EUR 1,000
// portfolio with no keys. Onboarding, the books, the policy, the plan and the
// readiness views all work offline, what is missing is said, and nothing is
// requested from the network, charged or ordered.
func TestAcceptanceEmptyEURPortfolioMakesNoRequest(t *testing.T) {
	e := newTestEnv(t)
	ct := &countingTransport{}
	client, err := httpx.New(httpx.Config{UserAgent: "stoxicity-test/0 (tester@example.com)", Transport: ct, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) result {
		return runWith(t, runOptions{cwd: e.dir, client: client, now: func() time.Time { return testNow }}, append([]string{"-config-dir", e.dir}, args...)...)
	}
	inst := writeInstruments(t, e)
	run("instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	run("portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	run("portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	run("portfolio", "show", "main/core").wantOut(t, "1000.00 EUR")
	run("portfolio", "verify").wantCode(t, exitOK)

	// the policy example is a valid draft that says which account facts are unknown
	run("policy", "example").wantCode(t, exitOK)
	run("plan", "list").wantOut(t, "no open plans")
	run("income", "expected", "main/core").wantOut(t, "no declared or estimated payout")
	r := run("performance", "main/core")
	r.wantCode(t, exitOK)
	run("order", "list").wantCode(t, exitOK)
	run("tier", "status").wantCode(t, exitOK)
	run("doctor").wantCode(t, exitOK)
	run("ops", "health").wantCode(t, exitOK)
	run("alerts", "status").wantCode(t, exitOK)
	run("daily").wantCode(t, exitOK)

	// nothing was ordered, so there is no order and the broker is offline
	run("broker", "status").wantOut(t, "OFFLINE", "nothing is sent")
	if len(ct.urls) != 0 {
		t.Fatalf("the offline workflow requested %v", ct.urls)
	}
}
