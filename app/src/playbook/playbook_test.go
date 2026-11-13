package playbook

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
)

// Invented data only.

func testPlaybook() Playbook {
	return Playbook{ID: "pb", Study: "st", Rules: []Rule{{
		ID: "r1", Types: []string{"earnings"},
		When:     []eventstudy.Condition{{Field: "number:surprise", Op: "gt", Value: 0.05}},
		Evidence: Evidence{Sessions: 5, MinEvents: 30, Direction: "up", MinEdge: 0.01},
		Action:   Buy,
	}}}
}

func testReport(status string, lower float64, dataHash string) eventstudy.Report {
	g := eventstudy.Group{Type: "earnings", Sessions: 5, Period: eventstudy.Validation, Dimension: "all", Bucket: "all",
		Net:    eventstudy.Stats{N: 50, Mean: 0.03, Lower: lower, Upper: 0.05},
		Status: status}
	if status != eventstudy.Evaluated {
		g.Why = "synthetic data"
	}
	return eventstudy.Report{Version: eventstudy.Version, Spec: eventstudy.Spec{ID: "st", Entry: eventstudy.NextOpen, Where: []eventstudy.Condition{{Field: "number:surprise", Op: "gt", Value: 0.05}}},
		SpecHash: "spec", DataHash: dataHash, Groups: []eventstudy.Group{g}}
}

func obs(typ string, numbers map[string]float64) eventstudy.Observation {
	return eventstudy.Observation{ID: "o1", Security: "AAA", Type: typ, AvailableAt: time.Date(2026, 1, 5, 21, 0, 0, 0, time.UTC), Numbers: numbers}
}

func TestEvaluate(t *testing.T) {
	surprise := map[string]float64{"surprise": 0.1}
	tests := []struct {
		name    string
		o       eventstudy.Observation
		r       eventstudy.Report
		signals int
		action  Action
		failing string
	}{
		{"evaluated gives buy", obs("earnings", surprise), testReport(eventstudy.Evaluated, 0.02, "d"), 1, Buy, ""},
		{"experimental gives watch", obs("earnings", surprise), testReport(eventstudy.Experimental, 0.02, "d"), 1, Watch, "evaluated"},
		{"bound below edge gives watch", obs("earnings", surprise), testReport(eventstudy.Evaluated, 0.005, "d"), 1, Watch, "edge"},
		{"condition that does not hold", obs("earnings", map[string]float64{"surprise": 0.01}), testReport(eventstudy.Evaluated, 0.02, "d"), 0, "", ""},
		{"unknown condition gives watch", obs("earnings", nil), testReport(eventstudy.Evaluated, 0.02, "d"), 1, Watch, "condition"},
		{"other type gives none", obs("guidance", surprise), testReport(eventstudy.Evaluated, 0.02, "d"), 0, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Evaluate(testPlaybook(), tc.o, eventstudy.Context{}, tc.r)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.signals {
				t.Fatalf("got %d signals, want %d", len(got), tc.signals)
			}
			if tc.signals == 0 {
				return
			}
			s := got[0]
			if s.Action != tc.action || s.Requested != Buy {
				t.Fatalf("action %s requested %s, want %s requested buy", s.Action, s.Requested, tc.action)
			}
			if tc.failing != "" {
				named := false
				for _, x := range s.Tests {
					if x.Name == tc.failing && x.Result != Pass {
						named = true
					}
				}
				if !named || !strings.Contains(s.Explanation, tc.failing) {
					t.Fatalf("test %q not named as the reason: %+v / %s", tc.failing, s.Tests, s.Explanation)
				}
			}
		})
	}
}

func TestSignalIDIsStable(t *testing.T) {
	o := obs("earnings", map[string]float64{"surprise": 0.1})
	a, _ := Evaluate(testPlaybook(), o, eventstudy.Context{}, testReport(eventstudy.Evaluated, 0.02, "d"))
	b, _ := Evaluate(testPlaybook(), o, eventstudy.Context{}, testReport(eventstudy.Evaluated, 0.02, "d"))
	c, _ := Evaluate(testPlaybook(), o, eventstudy.Context{}, testReport(eventstudy.Evaluated, 0.02, "other"))
	if a[0].ID != b[0].ID || !strings.HasPrefix(a[0].ID, "sig_") || len(a[0].ID) != 20 {
		t.Fatalf("ids %q and %q", a[0].ID, b[0].ID)
	}
	if a[0].ID == c[0].ID {
		t.Fatal("a different data hash gave the same id")
	}
}

func TestValidateRefusesBuyDown(t *testing.T) {
	p := testPlaybook()
	p.Rules[0].Evidence.Direction = "down"
	if err := p.Validate(); err == nil || !strings.HasPrefix(err.Error(), "playbook: ") {
		t.Fatalf("got %v", err)
	}
	if _, err := Evaluate(p, obs("earnings", nil), eventstudy.Context{}, eventstudy.Report{}); err == nil {
		t.Fatal("evaluate accepted an invalid playbook")
	}
}

type yesGate struct{}

func (yesGate) Approve(Signal) Decision { return Decision{Approved: true, Mode: "paper", By: "test"} }

func TestApprove(t *testing.T) {
	if d := Approve(yesGate{}, Signal{Action: Watch}); d.Approved {
		t.Fatal("a watch signal was approved")
	}
	if d := Approve(nil, Signal{Action: Buy}); d.Approved {
		t.Fatal("approved with no gate")
	}
	if d := Approve(yesGate{}, Signal{Action: Buy}); !d.Approved {
		t.Fatal("the gate's approval was lost")
	}
}

// A rule that selects more narrowly than the study measured must stay at watch:
// the study's figures describe a wider set of events.
func TestRuleNarrowerThanTheStudyStaysAtWatch(t *testing.T) {
	r := testReport(eventstudy.Evaluated, 0.02, "d")
	r.Spec.Where = nil
	got, err := Evaluate(testPlaybook(), obs("earnings", map[string]float64{"surprise": 0.1}), eventstudy.Context{}, r)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d signals, %v", len(got), err)
	}
	if got[0].Action != Watch {
		t.Errorf("action %s, want watch", got[0].Action)
	}
	failed := false
	for _, x := range got[0].Tests {
		if x.Name == "population" && x.Result == Fail {
			failed = true
		}
	}
	if !failed {
		t.Errorf("the population test did not fail: %+v", got[0].Tests)
	}
}

func TestTradeValidate(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		trade  Trade
		want   string // part of the error; empty when it is valid
	}{
		{"limit and stop", Buy, Trade{Notional: "1000", LimitOffset: 0.005, InvalidationOffset: 0.08}, ""},
		{"no stop, limit at the close", Buy, Trade{Notional: "250.50"}, ""},
		{"no notional", Buy, Trade{LimitOffset: 0.01}, "notional"},
		{"zero notional", Buy, Trade{Notional: "0.00"}, "notional"},
		{"negative notional", Buy, Trade{Notional: "-5"}, "notional"},
		{"notional with an exponent", Buy, Trade{Notional: "1e3"}, "notional"},
		{"notional ending in a point", Buy, Trade{Notional: "10."}, "notional"},
		{"limit below the close", Buy, Trade{Notional: "1000", LimitOffset: -0.01}, "limit_offset"},
		{"limit far above the close", Buy, Trade{Notional: "1000", LimitOffset: 0.5}, "limit_offset"},
		{"stop above the close", Buy, Trade{Notional: "1000", InvalidationOffset: -0.1}, "invalidation_offset"},
		{"stop below any price", Buy, Trade{Notional: "1000", InvalidationOffset: 1}, "invalidation_offset"},
		{"a sell is not sized", Sell, Trade{Notional: "1000"}, "never a short"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := testPlaybook()
			p.Rules[0].Action, p.Rules[0].Trade = tc.action, &tc.trade
			if tc.action == Sell {
				p.Rules[0].Evidence.Direction = "down"
			}
			err := p.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error %v does not mention %q", err, tc.want)
			}
		})
	}
}

func TestTradeLeavesTheHashOfAPlaybookWithoutOne(t *testing.T) {
	p := testPlaybook()
	// The hash and the encoding of this playbook as they were before a rule
	// could carry a trade.
	const hash = "7b7e3659fe14c07d18988f0385511ecd72dedcd0aff9a582d5644d18bf20fab4"
	const body = `{"id":"pb","study":"st","rules":[{"id":"r1","types":["earnings"],"when":[{"field":"number:surprise","op":"gt","value":0.05}],"evidence":{"sessions":5,"min_events":30,"direction":"up","min_edge":0.01},"action":"buy"}]}`
	if got := p.Hash(); got != hash {
		t.Fatalf("hash %s, was %s before the trade stanza existed", got, hash)
	}
	if b, _ := json.Marshal(p); string(b) != body {
		t.Fatalf("encoding changed:\n%s", b)
	}
	with := testPlaybook()
	with.Rules[0].Trade = &Trade{Notional: "1000", LimitOffset: 0.005}
	if with.Hash() == hash {
		t.Fatal("a trade stanza must change the hash: its numbers are part of the rules")
	}
	other := testPlaybook()
	other.Rules[0].Trade = &Trade{Notional: "2000", LimitOffset: 0.005}
	if with.Hash() == other.Hash() {
		t.Fatal("two notionals gave one hash")
	}
}

// The id of this signal as it was computed before a signal stated its life and
// its exit: the new fields must not move it.
func TestSignalIDIsPinned(t *testing.T) {
	got, err := Evaluate(testPlaybook(), obs("earnings", map[string]float64{"surprise": 0.1}), eventstudy.Context{}, testReport(eventstudy.Evaluated, 0.02, "d"))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d signals, %v", len(got), err)
	}
	if want := "sig_5500a6905a50caf2"; got[0].ID != want {
		t.Fatalf("id %s, was %s before the life and exit fields", got[0].ID, want)
	}
	if !got[0].CreatedAt.IsZero() {
		t.Fatal("evaluate set a creation time: that is the caller's clock")
	}
}

func TestExpiresAt(t *testing.T) {
	ny := eventstudy.Session{Zone: "America/New_York", Open: "09:30", Close: "16:00"}
	at := func(zone string, y int, m time.Month, d, h int) time.Time {
		loc, _ := time.LoadLocation(zone)
		return time.Date(y, m, d, h, 0, 0, 0, loc)
	}
	tests := []struct {
		name string
		gap  int
		ses  eventstudy.Session
		o    eventstudy.Observation
		want time.Time
	}{
		{"no gap rule", 0, ny, obs("earnings", nil), time.Time{}},
		{"unreadable zone", 5, eventstudy.Session{Zone: "Nowhere/Land"}, obs("earnings", nil), time.Time{}},
		{"no zone", 5, eventstudy.Session{}, obs("earnings", nil), time.Time{}},
		// 21:00 UTC on Jan 5 is 16:00 in New York: the same day, so the last entry day is Jan 10.
		{"day in the exchange zone", 5, ny, obs("earnings", nil), at("America/New_York", 2026, 1, 11, 0)},
		// 02:00 UTC on Jan 6 is still Jan 5 in New York.
		{"late evening in New York is the earlier day", 5, ny, eventstudy.Observation{AvailableAt: time.Date(2026, 1, 6, 2, 0, 0, 0, time.UTC)}, at("America/New_York", 2026, 1, 11, 0)},
		{"day precision is the UTC day", 5, ny, eventstudy.Observation{AvailableAt: time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), DayPrecision: true}, at("UTC", 2026, 1, 12, 0)},
		{"day precision needs no zone", 3, eventstudy.Session{}, eventstudy.Observation{AvailableAt: time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), DayPrecision: true}, at("UTC", 2026, 1, 10, 0)},
		{"across the clock change", 3, ny, eventstudy.Observation{AvailableAt: time.Date(2026, 3, 6, 21, 0, 0, 0, time.UTC)}, at("America/New_York", 2026, 3, 10, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := testReport(eventstudy.Evaluated, 0.02, "d")
			r.Spec.MaxEntryGapDays, r.Spec.Session = tc.gap, tc.ses
			o := tc.o
			o.ID, o.Security, o.Type = "o1", "AAA", "earnings"
			o.Numbers = map[string]float64{"surprise": 0.1}
			got, err := Evaluate(testPlaybook(), o, eventstudy.Context{}, r)
			if err != nil || len(got) != 1 {
				t.Fatalf("got %d signals, %v", len(got), err)
			}
			if !got[0].ExpiresAt.Equal(tc.want) {
				t.Fatalf("expires %v, want %v", got[0].ExpiresAt, tc.want)
			}
		})
	}
}

func TestExit(t *testing.T) {
	stop := &Trade{Notional: "1000", InvalidationOffset: 0.08}
	tests := []struct {
		name    string
		entry   eventstudy.Entry
		trade   *Trade
		status  string
		want    string // part of the text
		wantInv float64
	}{
		{"open entry with a stop", eventstudy.NextOpen, stop, eventstudy.Evaluated, "session 5, counting the entry session as the first; the stop lies 8.00% below", 0.08},
		{"close entry without a trade", eventstudy.NextClose, nil, eventstudy.Evaluated, "counting from the session after the entry; no stop is set", 0},
		{"trade without a stop", eventstudy.NextOpen, &Trade{Notional: "1000"}, eventstudy.Evaluated, "no stop is set", 0},
		{"watch proposes no position", eventstudy.NextOpen, stop, eventstudy.Experimental, "No position is proposed", 0.08},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := testPlaybook()
			p.Rules[0].Trade = tc.trade
			r := testReport(tc.status, 0.02, "d")
			r.Spec.Entry = tc.entry
			got, err := Evaluate(p, obs("earnings", map[string]float64{"surprise": 0.1}), eventstudy.Context{}, r)
			if err != nil || len(got) != 1 {
				t.Fatalf("got %d signals, %v", len(got), err)
			}
			x := got[0].Exit
			if x.Sessions != 5 || !strings.Contains(x.Text, tc.want) {
				t.Fatalf("exit %+v does not hold 5 sessions and %q", x, tc.want)
			}
			if (tc.wantInv == 0) != (x.Invalidation == nil) || (x.Invalidation != nil && *x.Invalidation != tc.wantInv) {
				t.Fatalf("invalidation %v, want %v", x.Invalidation, tc.wantInv)
			}
		})
	}
}

func TestSignalWithoutLifeFieldsStillDecodes(t *testing.T) {
	var s Signal
	if err := json.Unmarshal([]byte(`{"id":"sig_x","playbook":"pb","rule":"r1","action":"buy","sessions":5}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Exit.Sessions != 0 || !s.CreatedAt.IsZero() || !s.ExpiresAt.IsZero() {
		t.Fatalf("an older signal decoded with life fields: %+v", s)
	}
	b, _ := json.Marshal(Signal{})
	if strings.Contains(string(b), "created_at") || strings.Contains(string(b), "expires_at") {
		t.Fatalf("empty times are written: %s", b)
	}
}

func TestExtraDimensions(t *testing.T) {
	report := func(on bool) eventstudy.Report {
		r := testReport(eventstudy.Evaluated, 0.02, "d")
		r.Groups[0].Dimension, r.Groups[0].Bucket = "liquidity", "high"
		r.Spec.Where = nil
		if on {
			r.Spec.Context = eventstudy.ContextSpec{LiquiditySessions: 20, LiquidityEdges: []float64{1, 2}}
		}
		return r
	}
	rule := func(dimension string) Playbook {
		p := testPlaybook()
		p.Rules[0].When = nil
		p.Rules[0].Evidence.Dimension = dimension
		return p
	}
	tests := []struct {
		name      string
		dimension string
		on        bool
		invalid   bool
		result    Result
		detail    string
	}{
		{"liquidity is a dimension and the study turns it on", "liquidity", true, false, Pass, ""},
		{"relative momentum the study left off", "relative_momentum", true, false, Fail, "does not break its groups down by relative_momentum"},
		{"liquidity the study did not break down", "liquidity", false, false, Fail, "does not break its groups down by liquidity"},
		{"a name that is no dimension", "colour", true, true, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := eventstudy.Context{Liquidity: "high", RelativeMomentum: "high"}
			got, err := Evaluate(rule(tc.dimension), obs("earnings", nil), ctx, report(tc.on))
			if tc.invalid {
				if err == nil {
					t.Fatal("evaluate accepted a dimension that does not exist")
				}
				return
			}
			if err != nil || len(got) != 1 {
				t.Fatalf("got %d signals, %v", len(got), err)
			}
			var found bool
			for _, x := range got[0].Tests {
				if x.Name == "comparable" {
					found = true
					if x.Result != tc.result || !strings.Contains(x.Detail, tc.detail) {
						t.Fatalf("comparable is %s (%s), want %s with %q", x.Result, x.Detail, tc.result, tc.detail)
					}
				}
			}
			if !found || (tc.result != Pass && got[0].Action != Watch) {
				t.Fatalf("comparable test missing or the answer was %s", got[0].Action)
			}
		})
	}
}
