package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/walkfwd"
)

func wfWeekdays(start string, n int) []string {
	t, _ := time.Parse(time.DateOnly, start)
	var out []string
	for len(out) < n {
		if w := t.Weekday(); w != time.Saturday && w != time.Sunday {
			out = append(out, t.Format(time.DateOnly))
		}
		t = t.AddDate(0, 0, 1)
	}
	return out
}

// wfData is five securities that each rise after a decision on the first of
// alternate months of 2022, and a flat fund.
func wfData(t *testing.T, e *testEnv) (bars, props string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("security,day,open,high,low,close,volume,currency\n")
	var ps []walkfwd.Proposal
	for i := 0; i < 5; i++ {
		days := wfWeekdays(time.Date(2022, time.Month(3+i*2), 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), 30)
		sec := fmt.Sprintf("sec%d", i)
		for j, d := range days {
			px := "100"
			if j >= 3 {
				px = "110"
			}
			fmt.Fprintf(&b, "%s,%s,%s,112,99,%s,1000000,USD\n", sec, d, px, px)
		}
		ps = append(ps, walkfwd.Proposal{ID: "p" + sec, Trial: "t1", Security: sec, Decided: days[0], KnownOn: days[0], Limit: money.MustDecimal("100.5"), Qty: money.MustDecimal("10"), HorizonSessions: 8, Currency: "USD"})
	}
	for _, d := range wfWeekdays("2022-03-01", 400) {
		fmt.Fprintf(&b, "fund,%s,50,50,50,50,1,USD\n", d)
	}
	raw, _ := json.Marshal(ps)
	return writeFile(t, e, "bars.csv", b.String()), writeFile(t, e, "props.json", string(raw))
}

func TestWalkfwdTrialsSplitsAndFreezeGateTheTestPeriod(t *testing.T) {
	e := newTestEnv(t)
	bars, props := wfData(t, e)
	manifest := writeFile(t, e, "manifest.json", `{"kind":"known","synthetic":false,"point_in_time_universe":true,"includes_delisted":true,"fund_security":"fund"}`)
	splits := writeFile(t, e, "splits.json", `{"train":{"from":"2020-01-01","to":"2020-12-31"},"tune":{"from":"2021-02-01","to":"2021-12-31"},"test":{"from":"2022-02-01","to":"2022-12-31"},"reserve":{"from":"2023-02-01","to":"2023-12-31"},"embargo_days":20}`)
	crit := writeFile(t, e, "crit.json", `{"min_matured_trades":4,"min_clusters":4,"min_coverage":0.8,"min_fill_rate":0.5,"min_excess_lower_bound":0,"max_worst_loss":0.3,"alpha":0.05}`)
	trial := writeFile(t, e, "trial.json", `{"id":"t1","strategy":"earnings-followthrough-paper-v1","params":"lookback=20"}`)
	run := func(period string) result {
		return e.run(t, "walkfwd", "run", "-trial", "t1", "-period", period, "-manifest", manifest, "-proposals", props, "-bars", bars)
	}

	e.run(t, "walkfwd", "example").wantOut(t, "trial.json", "embargo", "REPLACE")
	// an unregistered trial does not run
	run("tune").wantCode(t, exitError)
	e.run(t, "walkfwd", "register", "-file", trial).wantOut(t, "1 trial(s)")
	other := writeFile(t, e, "trial2.json", `{"id":"t1","strategy":"earnings-followthrough-paper-v1","params":"lookback=30"}`)
	e.run(t, "walkfwd", "register", "-file", other).wantCode(t, exitError)

	e.run(t, "walkfwd", "splits", "-file", splits).wantCode(t, exitOK)
	e.run(t, "walkfwd", "criteria", "-file", crit).wantCode(t, exitOK)
	// the test period is not readable before the freeze
	run("test").wantCode(t, exitError)
	e.run(t, "walkfwd", "freeze").wantOut(t, "frozen")
	e.run(t, "walkfwd", "splits", "-file", splits).wantCode(t, exitError)
	e.run(t, "walkfwd", "criteria", "-file", crit).wantCode(t, exitError)

	r := run("test")
	r.wantCode(t, exitOK)
	r.wantOut(t, "EVALUATED", "trial t1", "known replay", "independent clusters", "extra slippage")
	// reading it again is flagged
	r = run("test")
	r.wantOut(t, "EXPERIMENTAL", "optimised against it")
	// the reserve once
	run("reserve").wantCode(t, exitOK)
	run("reserve").wantCode(t, exitError)

	// a trial registered later raises the bar and is counted
	later := writeFile(t, e, "trial3.json", `{"id":"t2","strategy":"x","params":"y"}`)
	e.run(t, "walkfwd", "register", "-file", later).wantOut(t, "2 trial(s)", "after the freeze")
	e.run(t, "walkfwd", "status").wantOut(t, "2 trial(s)", "t1", "t2", "not run", "frozen")
	r = e.run(t, "walkfwd", "run", "-trial", "t1", "-period", "tune", "-manifest", manifest, "-proposals", props, "-bars", bars, "-json")
	r.wantOut(t, `"trials_registered": 2`, `"status"`)
}

func TestWalkfwdEventProposalsBecomeReplayProposalsIncludingNoTrade(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "walkfwd", "from-events", "-trial", "t1")
	r.wantCode(t, exitOK)
	r.wantOut(t, "null")
	e.run(t, "walkfwd", "from-events").wantCode(t, exitUsage)
}
