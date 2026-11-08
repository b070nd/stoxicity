package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// eventBars is 20 quiet sessions at about 100 and then the event session and the next.
func eventBars() string {
	var b strings.Builder
	b.WriteString("security,day,open,high,low,close,volume,currency\n")
	t := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var days []string
	for len(days) < 22 {
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday {
			days = append(days, t.Format(time.DateOnly))
		}
		t = t.AddDate(0, 0, 1)
	}
	for i, d := range days[:20] {
		c := "100"
		if i%2 == 1 {
			c = "99.5"
		}
		fmt.Fprintf(&b, "sec_x,%s,%s,101,99,%s,100000,USD\n", d, c, c)
	}
	fmt.Fprintf(&b, "sec_x,%s,106,112,105,110,200000,USD\n", days[20])
	fmt.Fprintf(&b, "sec_x,%s,110,112,105,110,150000,USD\n", days[21])
	return b.String()
}

const eventJSON = `{"id":"ev1","security":"sec_x","issuer":"iss_x","security_issuer":"iss_x","kind":"earnings","day":"2026-09-29","timing":"pre","status":"completed","verified":%t,"source":"company release","ref":"10-Q Q3","origin":%q,"comparable_years":3,"results_ref":"scorecard","split_consistent":true,"price_ref":"vendor adjusted file"}`

func TestEventSleeveIsResearchUntilReviewedAndNeverReal(t *testing.T) {
	e := newTestEnv(t)
	bars := writeFile(t, e, "bars.csv", eventBars())
	ev := writeFile(t, e, "ev1.json", fmt.Sprintf(eventJSON, true, "origin-1"))
	fees := writeFile(t, e, "fees.json", `{"known": true, "rate": "0.001", "min": "1.00 USD", "fixed": "0.00 USD", "source": "test schedule"}`)

	// without a fee schedule, cost feasibility is unknown: research only, no quantity
	r := e.run(t, "event", "evaluate", "-event", ev, "-bars", bars, "-equity", "100000.00 USD", "-as-of", "2026-10-05")
	r.wantCode(t, exitOK)
	r.wantOut(t, "RESEARCH", "EXPERIMENTAL", "never offered for a real account")
	if strings.Contains(r.out, "paper candidate") {
		t.Fatalf("a candidate without a known fee schedule:\n%s", r.out)
	}

	// with fees but an unreviewed configuration: still research
	r = e.run(t, "event", "evaluate", "-event", ev, "-bars", bars, "-equity", "100000.00 USD", "-fees", fees, "-as-of", "2026-10-05")
	r.wantCode(t, exitOK)
	r.wantOut(t, "RESEARCH")

	r = e.run(t, "event", "config", "review", "-by", "tester")
	r.wantCode(t, exitOK)
	r = e.run(t, "event", "evaluate", "-event", ev, "-bars", bars, "-equity", "100000.00 USD", "-fees", fees, "-as-of", "2026-10-05")
	r.wantCode(t, exitOK)
	r.wantOut(t, "PAPER", "paper candidate", "invalidation", "never moved into the core")

	r = e.run(t, "event", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "ev1", "sec_x", "PAPER")
}

func TestEventSleeveRepeatedStoryWaitsAndExitsAreDistinct(t *testing.T) {
	e := newTestEnv(t)
	bars := writeFile(t, e, "bars.csv", eventBars())
	fees := writeFile(t, e, "fees.json", `{"known": true, "rate": "0.001", "min": "1.00 USD", "fixed": "0.00 USD", "source": "test schedule"}`)
	e.run(t, "event", "config", "review", "-by", "tester").wantCode(t, exitOK)
	ev1 := writeFile(t, e, "ev1.json", fmt.Sprintf(eventJSON, true, "same-story"))
	e.run(t, "event", "evaluate", "-event", ev1, "-bars", bars, "-equity", "100000.00 USD", "-fees", fees, "-as-of", "2026-10-05").wantOut(t, "PAPER")

	ev2 := writeFile(t, e, "ev2.json", strings.Replace(fmt.Sprintf(eventJSON, true, "same-story"), `"ev1"`, `"ev2"`, 1))
	r := e.run(t, "event", "evaluate", "-event", ev2, "-bars", bars, "-equity", "100000.00 USD", "-fees", fees, "-as-of", "2026-10-05")
	r.wantCode(t, exitOK)
	r.wantOut(t, "WAIT", "already used")

	r = e.run(t, "event", "review", "-proposal", "ev1", "-entry-day", "2026-10-01", "-entry-price", "111", "-bars", bars, "-as-of", "2026-10-02")
	r.wantCode(t, exitOK)
	r.wantOut(t, "HOLD")
	r = e.run(t, "event", "review", "-proposal", "ev1", "-entry-day", "2026-09-29", "-entry-price", "111", "-bars", bars, "-as-of", "2026-10-14")
	r.wantCode(t, exitOK)
	r.wantOut(t, "TIME_EXIT", "Nothing was sold")

	// wrong usage
	e.run(t, "event", "evaluate").wantCode(t, exitUsage)
	e.run(t, "event", "bogus").wantCode(t, exitUsage)
	e.run(t, "event", "example").wantOut(t, "REPLACE")
}
