package playbook

import (
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
