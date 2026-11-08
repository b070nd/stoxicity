package eventstrat

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/allocate"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/paper"
)

func d(s string) money.Decimal { return money.MustDecimal(s) }

func weekdays(start string, n int) []string {
	t, _ := time.Parse(time.DateOnly, start)
	var out []string
	for len(out) < n {
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday {
			out = append(out, t.Format(time.DateOnly))
		}
		t = t.AddDate(0, 0, 1)
	}
	return out
}

// series is 20 quiet sessions at 100 and then the event session.
func series(eventOpen, eventHigh, eventLow, eventClose string, eventVol int64) ([]paper.Bar, string) {
	days := weekdays("2026-09-01", 22)
	var bars []paper.Bar
	for i, day := range days[:20] {
		c := d("100")
		if i%2 == 1 {
			c = d("99.5")
		}
		bars = append(bars, paper.Bar{Security: "sec_x", Day: day, Open: c, High: d("101"), Low: d("99"), Close: c, Volume: 100000, Currency: "USD"})
	}
	bars = append(bars, paper.Bar{Security: "sec_x", Day: days[20], Open: d(eventOpen), High: d(eventHigh), Low: d(eventLow), Close: d(eventClose), Volume: eventVol, Currency: "USD"})
	bars = append(bars, paper.Bar{Security: "sec_x", Day: days[21], Open: d(eventClose), High: d(eventHigh), Low: d(eventLow), Close: d(eventClose), Volume: 150000, Currency: "USD"})
	return bars, days[20]
}

func base(t *testing.T) Input {
	t.Helper()
	bars, evDay := series("106", "112", "105", "110", 200000)
	cfg := DefaultConfig()
	cfg.Reviewed = true
	eq, _ := money.FromMinor(10000000, "USD")
	cash, _ := money.FromMinor(5000000, "USD")
	return Input{
		Config: cfg,
		Event: Event{ID: "ev1", Security: "sec_x", Issuer: "iss_x", SecurityIssuer: "iss_x", Kind: "earnings", Day: evDay, Timing: "pre", Status: "completed",
			Verified: true, Source: "company release", Ref: "10-Q Q3", Origin: "origin-1", ComparableYears: 3, ResultsRef: "scorecard sec_x", SplitConsistent: true, PriceRef: "vendor adjusted file"},
		Bars:    bars,
		Account: Account{Equity: eq, Cash: cash, Fees: allocate.Fees{Known: true, Rate: d("0.001"), Min: 100, Source: "test schedule"}},
		AsOf:    "2026-10-05",
	}
}

func result(p Proposal, name string) Result {
	for _, t := range p.Tests {
		if t.Name == name {
			return t.Result
		}
	}
	return "absent"
}

func eval(t *testing.T, in Input) Proposal {
	t.Helper()
	p, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestACompleteVerifiedEventWithFollowThroughIsOnlyAPaperCandidate(t *testing.T) {
	p := eval(t, base(t))
	if p.Status != Paper || !p.Experimental || p.RealAllowed {
		t.Fatalf("%+v", p)
	}
	if p.Qty == nil || p.Qty.String() != "37" || p.EntryLimit.String() != "111.10" || p.Invalidation.String() != "105" {
		t.Fatalf("%v %v %v", p.Qty, p.EntryLimit, p.Invalidation)
	}
	// planned loss, not guaranteed loss: 37 shares x (6.10 + 0.5555) rounded against the trade
	if p.PlannedLoss == nil || p.PlannedLoss.String() != "246.42 USD" {
		t.Fatalf("%v", p.PlannedLoss)
	}
	if p.ProfitReview == nil || p.ProfitReview.String() != "123.30" {
		t.Fatalf("2R review level: %v", p.ProfitReview)
	}
	if p.ReviewSessions != 10 || p.Catalyst == "" || p.Horizon == "" || p.InvalidationBy == "" {
		t.Fatalf("an event idea states catalyst, invalidation and horizon: %+v", p)
	}
	if p.EntryNotBefore <= p.EventSession {
		t.Fatalf("entry may not be before the session after the event: %s vs %s", p.EntryNotBefore, p.EventSession)
	}
	for _, name := range []string{"source verified", "close above prior maximum", "volume surge", "cost feasible", "affordable size"} {
		if result(p, name) != Pass {
			t.Fatalf("%s: %+v", name, p.Tests)
		}
	}
	// the same input gives the same manifest; any input change gives another
	q := eval(t, base(t))
	if q.ManifestHash != p.ManifestHash {
		t.Fatal("not reproducible")
	}
	in := base(t)
	in.Event.Ref = "other"
	if eval(t, in).ManifestHash == p.ManifestHash {
		t.Fatal("manifest ignores its input")
	}
}

func TestUnknownRequiredInputsMeanResearchOnly(t *testing.T) {
	cases := map[string]func(*Input){
		"unreviewed settings": func(in *Input) { in.Config.Reviewed = false },
		"headline only":       func(in *Input) { in.Event.Verified, in.Event.Source, in.Event.Ref = false, "", "" },
		"unresolved issuer":   func(in *Input) { in.Event.CounterpartyUnresolved = true },
		"timing unknown":      func(in *Input) { in.Event.Timing = "unknown" },
		"splits not stated":   func(in *Input) { in.Event.SplitConsistent = false },
		"too little history":  func(in *Input) { in.Event.ComparableYears = 1 },
		"no fee schedule":     func(in *Input) { in.Account.Fees = allocate.Fees{} },
		"origin unknown":      func(in *Input) { in.Event.Origin = "" },
		"no equity":           func(in *Input) { in.Account.Equity = money.Amount{} },
		"currency not given":  func(in *Input) { in.Account.Equity, _ = money.FromMinor(10000000, "EUR") },
		"event not in series": func(in *Input) { in.Bars = in.Bars[:20] },
		"short price history": func(in *Input) { in.Bars = in.Bars[8:] },
	}
	for name, edit := range cases {
		in := base(t)
		edit(&in)
		p := eval(t, in)
		if p.Status != Research {
			t.Errorf("%s: %s %v", name, p.Status, p.Reasons)
		}
		if p.Qty != nil {
			t.Errorf("%s: sized a trade it could not support", name)
		}
	}
}

func TestEvaluatedFailuresWait(t *testing.T) {
	cases := map[string]func(*Input){
		"duplicate origin":     func(in *Input) { in.Account.SeenOrigins = []string{"origin-1"} },
		"correction":           func(in *Input) { in.Event.CorrectedOn = "2026-09-30" },
		"event only scheduled": func(in *Input) { in.Event.Status = "scheduled" },
		"rumoured":             func(in *Input) { in.Event.Status = "rumoured" },
		"contradiction":        func(in *Input) { in.Event.Contradiction = true },
		"other issuer":         func(in *Input) { in.Event.SecurityIssuer = "iss_y" },
		"session not finished": func(in *Input) { in.AsOf = in.Bars[20].Day },
		"volume too low":       func(in *Input) { in.Bars[20].Volume = 140000 },
		"no new high": func(in *Input) {
			in.Bars[20].Close = d("99.9")
			in.Bars[20].High = d("112")
			in.Bars[20].Low = d("99")
		},
		"illiquid":      func(in *Input) { in.Config.MinAvgDailyValue = d("50000000") },
		"risk too wide": func(in *Input) { in.Config.MaxRisk = d("0.01") },
	}
	for name, edit := range cases {
		in := base(t)
		edit(&in)
		p := eval(t, in)
		if p.Status != Wait {
			t.Errorf("%s: %s %v", name, p.Status, p.Reasons)
		}
	}
}

func TestPostCloseResultsReactInTheNextSession(t *testing.T) {
	in := base(t)
	// released after the close of the day before the event bar: the reacting session is the event bar
	in.Event.Timing = "post"
	in.Event.Day = in.Bars[19].Day
	p := eval(t, in)
	if p.Status != Paper || p.EventSession != in.Bars[20].Day {
		t.Fatalf("%s %s %v", p.Status, p.EventSession, p.Reasons)
	}
	// results released after the close of the event bar react in the one after, which here has no history of its own
	in.Event.Day = in.Bars[20].Day
	if p := eval(t, in); p.EventSession != in.Bars[21].Day {
		t.Fatalf("%s", p.EventSession)
	}
}

func TestAGapIsFlaggedAndTheInvalidationMustStillExist(t *testing.T) {
	in := base(t)
	in.Bars[20].Open, in.Bars[20].High, in.Bars[20].Low, in.Bars[20].Close = d("120"), d("125"), d("105"), d("112")
	p := eval(t, in)
	if result(p, "opening gap") != Warn {
		t.Fatalf("%+v", p.Tests)
	}
	if p.Status != Paper {
		t.Fatalf("a gap alone does not block, the risk distance decides: %s %v", p.Status, p.Reasons)
	}
	// a session that closed on its low has nothing to invalidate against: stop-based sizing is unavailable
	in = base(t)
	in.Bars[20].Low, in.Bars[20].Open = d("110"), d("110")
	p = eval(t, in)
	if p.Status != Research || p.Invalidation != nil || p.Qty != nil {
		t.Fatalf("%s %v", p.Status, p.Reasons)
	}
	if !strings.Contains(strings.Join(p.Reasons, ";"), "stop-based sizing is unavailable") {
		t.Fatalf("%v", p.Reasons)
	}
}

func TestCostInfeasibleAndUnaffordableTradesWait(t *testing.T) {
	in := base(t)
	in.Account.Fees = allocate.Fees{Known: true, Rate: d("0.001"), Min: 5000, Source: "test"}
	p := eval(t, in)
	if p.Status != Wait || result(p, "cost feasible") != Fail || !strings.Contains(strings.Join(p.Reasons, ";"), "round-trip fees") {
		t.Fatalf("%s %v", p.Status, p.Reasons)
	}
	in = base(t)
	in.Account.Equity, _ = money.FromMinor(200000, "USD") // 2,000.00
	p = eval(t, in)
	if p.Status != Wait || result(p, "affordable size") != Fail {
		t.Fatalf("%s %v", p.Status, p.Reasons)
	}
	// a smaller policy sleeve lowers the size, never raises it
	in = base(t)
	in.Account.PolicySleeve = d("0.01")
	p = eval(t, in)
	if p.Qty == nil || p.Qty.String() != "9" {
		t.Fatalf("%v %v", p.Qty, p.Reasons)
	}
	in.Account.PolicySleeve = d("0.9")
	if q := eval(t, in); q.Qty.String() != "37" {
		t.Fatalf("a policy cannot raise the configured sleeve: %v", q.Qty)
	}
}

func TestConfigValidation(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Config){
		"review too short":  func(c *Config) { c.ReviewSessions = 4 },
		"review too long":   func(c *Config) { c.ReviewSessions = 21 },
		"short lookback":    func(c *Config) { c.Lookback = 2 },
		"risk above sleeve": func(c *Config) { c.RiskFraction = d("0.5") },
		"wrong version":     func(c *Config) { c.Version = "x" },
		"zero volume rule":  func(c *Config) { c.VolumeMultiple = d("0") },
	} {
		cc := DefaultConfig()
		edit(&cc)
		if err := cc.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if DefaultConfig().Hash() != func() string { x := DefaultConfig(); x.Reviewed = true; return x.Hash() }() {
		t.Fatal("review state must not change the hash")
	}
}

func TestTimeStopIsADistinctExitAndGapsCannotExtendIt(t *testing.T) {
	h := Holding{Security: "sec_x", EntryDay: "2026-10-05", EntryPrice: d("111"), Invalidation: d("105"), ReviewSessions: 10}
	var bars []paper.Bar
	for _, day := range weekdays("2026-10-06", 12) {
		bars = append(bars, paper.Bar{Security: "sec_x", Day: day, Open: d("112"), High: d("114"), Low: d("110"), Close: d("112"), Volume: 1000, Currency: "USD"})
	}
	x, err := Review(h, bars, "2026-10-12")
	if err != nil || x.Kind != Hold || x.SessionsHeld != 4 {
		t.Fatalf("%+v %v", x, err)
	}
	x, _ = Review(h, bars, "2026-10-19")
	if x.Kind != Hold || x.SessionsHeld != 9 {
		t.Fatalf("%+v", x)
	}
	x, _ = Review(h, bars, "2026-10-20")
	if x.Kind != TimeExit || x.SessionsHeld != 10 || !strings.Contains(x.Reason, "not moved into the core") {
		t.Fatalf("%+v", x)
	}
	// the price file stops: the holding is still not extended
	x, _ = Review(h, bars[:2], "2026-10-26")
	if x.Kind != TimeExit || x.SessionsHeld != 14 {
		t.Fatalf("%+v", x)
	}
	x, _ = Review(h, nil, "2026-10-26")
	if x.Kind != TimeExit || len(x.Notes) == 0 {
		t.Fatalf("%+v", x)
	}
}

func TestInvalidationAndProfitReviewTakePrecedence(t *testing.T) {
	pr := d("123.30")
	h := Holding{Security: "sec_x", EntryDay: "2026-10-05", EntryPrice: d("111"), Invalidation: d("105"), ReviewSessions: 10, ProfitReview: &pr}
	days := weekdays("2026-10-06", 4)
	mk := func(i int, o, hi, lo, c string) paper.Bar {
		return paper.Bar{Security: "sec_x", Day: days[i], Open: d(o), High: d(hi), Low: d(lo), Close: d(c), Volume: 1000, Currency: "USD"}
	}
	// the market gaps down through the invalidation
	x, _ := Review(h, []paper.Bar{mk(0, "112", "113", "111", "112"), mk(1, "100", "104", "98", "101")}, "2026-10-09")
	if x.Kind != Invalidated || len(x.Notes) == 0 || !strings.Contains(x.Notes[0], "gapped") {
		t.Fatalf("%+v", x)
	}
	// reached the profit-review level: a review, not a forced sale
	x, _ = Review(h, []paper.Bar{mk(0, "112", "113", "111", "112"), mk(1, "120", "125", "119", "124")}, "2026-10-09")
	if x.Kind != ProfitReview || !strings.Contains(x.Reason, "a rule, not an expected return") {
		t.Fatalf("%+v", x)
	}
	// even after the time exit has passed, an invalidated position says so first
	late := mk(0, "100", "104", "98", "101")
	x, _ = Review(h, []paper.Bar{late}, "2026-11-30")
	if x.Kind != Invalidated {
		t.Fatalf("%+v", x)
	}
	if _, err := Review(h, nil, "2026-10-05"); err == nil {
		t.Fatal("a review on the entry day is not a review")
	}
	h.ReviewSessions = 3
	if _, err := Review(h, nil, "2026-10-20"); err == nil {
		t.Fatal("time exit outside 5-20")
	}
}
