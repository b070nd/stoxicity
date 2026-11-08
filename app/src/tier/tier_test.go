package tier

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

var now = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func ev(target int, item Item, met bool) Evidence {
	return Evidence{Target: target, Item: item, Met: met, Ref: "report 12", By: "me", At: now}
}

func all(target int) map[Item]Evidence {
	m := map[Item]Evidence{}
	for _, it := range Required(target) {
		m[it] = ev(target, it, true)
	}
	return m
}

func reviewed() Config {
	c := ExampleConfig()
	c.Reviewed = true
	return c
}

func TestPermissionsAreOperationalAndGrowWithTheTier(t *testing.T) {
	want := []Permissions{
		{Paper: true},
		{Paper: true, ManualReal: true},
		{Paper: true, ManualReal: true, BrokerSubmission: true, EventSleeveReal: true},
		{Paper: true, ManualReal: true, BrokerSubmission: true, EventSleeveReal: true},
	}
	for i, w := range want {
		if got := PermissionsOf(i); got != w {
			t.Errorf("tier %d: %+v", i, got)
		}
	}
	if Name(2) != "confirmed execution" || Name(9) != "unknown" {
		t.Fatal("names")
	}
}

func TestOnlyCompleteSourcedEvidenceWithReviewedThresholdsUnlocks(t *testing.T) {
	cfg := reviewed()
	r, err := Assess(2, all(2), cfg, 30)
	if err != nil || !r.Ready || len(r.Missing) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	// time alone, however long, is not readiness
	r, _ = Assess(2, nil, cfg, 100000)
	if r.Ready || len(r.Missing) != len(Required(2)) {
		t.Fatalf("%+v", r)
	}
	// one item recorded as not met, or missing, keeps it shut
	m := all(2)
	m[BrokerClean] = ev(2, BrokerClean, false)
	if r, _ = Assess(2, m, cfg, 30); r.Ready || !strings.Contains(strings.Join(r.Missing, ";"), "no_broker_discrepancy: recorded as not met") {
		t.Fatalf("%+v", r)
	}
	delete(m, FailureScenarios)
	if r, _ = Assess(2, m, cfg, 30); r.Ready {
		t.Fatal("a missing item unlocked")
	}
	// the observation floor is necessary...
	if r, _ = Assess(2, all(2), cfg, 29); r.Ready || !strings.Contains(strings.Join(r.Missing, ";"), "29 of 30") {
		t.Fatalf("%+v", r)
	}
	// ...and thresholds nobody reviewed are illustrations
	if r, _ = Assess(2, all(2), ExampleConfig(), 30); r.Ready || !strings.Contains(strings.Join(r.Missing, ";"), "illustrations you have not reviewed") {
		t.Fatalf("%+v", r)
	}
	// tier 3 needs its own review on top
	if r, _ = Assess(3, all(2), cfg, 30); r.Ready {
		t.Fatal("tier 3 without its review")
	}
	if r, _ = Assess(3, all(3), cfg, 30); !r.Ready {
		t.Fatalf("%+v", r)
	}
	if _, err := Assess(0, nil, cfg, 0); err == nil {
		t.Fatal("tier 0 has nothing to unlock")
	}
}

func TestEvidenceNeedsASourceAndTheRightTier(t *testing.T) {
	e := ev(1, ConfigComplete, true)
	if e.Validate() != nil {
		t.Fatal(e.Validate())
	}
	for name, mut := range map[string]func(*Evidence){
		"no source":  func(e *Evidence) { e.Ref = " " },
		"no one":     func(e *Evidence) { e.By = "" },
		"wrong tier": func(e *Evidence) { e.Item = BrokerClean },
	} {
		x := e
		mut(&x)
		if x.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDecisionIsBoundToTheEvidenceAndExpires(t *testing.T) {
	cfg := reviewed()
	r, _ := Assess(2, all(2), cfg, 30)
	d := Decision{Target: 2, EvidenceHash: r.Hash, By: "me", At: now, RiskText: RiskText(2)}
	if err := d.Allows(r, cfg, now.AddDate(0, 0, 6)); err != nil {
		t.Fatal(err)
	}
	if err := d.Allows(r, cfg, now.AddDate(0, 0, 8)); err == nil || !strings.Contains(err.Error(), "older than 7 days") {
		t.Fatalf("%v", err)
	}
	// new evidence after the decision makes it stale
	m := all(2)
	changed := ev(2, FailureScenarios, true)
	changed.Ref = "report 13"
	m[FailureScenarios] = changed
	r2, _ := Assess(2, m, cfg, 30)
	if err := d.Allows(r2, cfg, now); err == nil || !strings.Contains(err.Error(), "evidence changed") {
		t.Fatalf("%v", err)
	}
	// a decision for another tier, or for a tier that is not ready, allows nothing
	if err := (Decision{Target: 1}).Allows(r, cfg, now); err == nil {
		t.Fatal("wrong tier")
	}
	notReady, _ := Assess(2, nil, cfg, 0)
	if err := (Decision{Target: 2, EvidenceHash: notReady.Hash, At: now}).Allows(notReady, cfg, now); err == nil {
		t.Fatal("not ready")
	}
	if !strings.Contains(RiskText(2), "orders submitted through a broker after my confirmation") || strings.Contains(RiskText(1), "broker") {
		t.Fatal(RiskText(1))
	}
}

func TestConfigValidation(t *testing.T) {
	if ExampleConfig().Validate() != nil {
		t.Fatal("example")
	}
	for _, c := range []Config{
		{ObservationSessions: map[int]int{0: 1}, DecisionValidDays: 7},
		{ObservationSessions: map[int]int{2: -1}, DecisionValidDays: 7},
		{ObservationSessions: map[int]int{2: 30}, DecisionValidDays: 0},
	} {
		if c.Validate() == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestSuspensionAndDrawdownAreExact(t *testing.T) {
	sig := []Signal{{Kind: StaleInputs, Active: true, Reason: "quotes are 3 days old"}, {Kind: Drawdown, Active: false, Reason: "cleared"}, {Kind: Manual, Active: true, Reason: "away"}}
	got := Suspended(sig)
	if len(got) != 2 || !strings.HasPrefix(got[0], "manual") || !strings.HasPrefix(got[1], "stale_execution_inputs") {
		t.Fatalf("%v", got)
	}
	if len(Suspended(nil)) != 0 || !Drawdown.Valid() || Kind("x").Valid() {
		t.Fatal("kinds")
	}
	fall, peak := MaxDrawdown([]int64{100, 120, 90, 130, 117, 126})
	if fall != 30 || peak != 120 { // 25% from 120 to 90 beats 10% from 130 to 117
		t.Fatalf("%d %d", fall, peak)
	}
	if f, _ := MaxDrawdown([]int64{5, 6, 7}); f != 0 {
		t.Fatalf("a rising series fell by %d", f)
	}
	if f, p := MaxDrawdown(nil); f != 0 || p != 0 {
		t.Fatal("empty")
	}
	for _, c := range []struct {
		fall, peak int64
		th         string
		want       bool
	}{{30, 120, "0.25", true}, {30, 120, "0.2501", false}, {29, 120, "0.25", false}, {0, 0, "0.25", false}, {30, 120, "0", false}} {
		got, err := Breaches(c.fall, c.peak, money.MustDecimal(c.th))
		if err != nil || got != c.want {
			t.Errorf("%+v: %v %v", c, got, err)
		}
	}
}
