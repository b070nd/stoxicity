package eval

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPrecisionAtK(t *testing.T) {
	cases := []Case{{0.9, true}, {0.8, false}, {0.7, true}, {0.1, false}}
	if got := PrecisionAtK(cases, 2); !near(got, 0.5) {
		t.Errorf("P@2 = %v, want 0.5", got)
	}
	if got := PrecisionAtK(cases, 10); !near(got, 0.5) {
		t.Errorf("P@10 clamps to 4 cases = %v, want 0.5", got)
	}
	if got := PrecisionAtK(nil, 3); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
}

func TestBrier(t *testing.T) {
	if got := Brier([]Case{{1, true}, {0, false}}); !near(got, 0) {
		t.Errorf("perfect = %v, want 0", got)
	}
	if got := Brier([]Case{{0.5, true}, {0.5, false}}); !near(got, 0.25) {
		t.Errorf("coin flip = %v, want 0.25", got)
	}
}

func TestECE(t *testing.T) {
	// Says 0.9 and is right 9 times in 10: calibrated.
	var calibrated []Case
	for i := 0; i < 10; i++ {
		calibrated = append(calibrated, Case{0.9, i < 9})
	}
	if got := ECE(calibrated, 10); !near(got, 0) {
		t.Errorf("calibrated ECE = %v, want 0", got)
	}
	// Says 0.9 and is right half the time: off by 0.4.
	var over []Case
	for i := 0; i < 10; i++ {
		over = append(over, Case{0.9, i < 5})
	}
	if got := ECE(over, 10); !near(got, 0.4) {
		t.Errorf("overconfident ECE = %v, want 0.4", got)
	}
	// A score of exactly 1 lands in the last bin, not out of range.
	if got := ECE([]Case{{1, true}}, 10); !near(got, 0) {
		t.Errorf("score 1 ECE = %v, want 0", got)
	}
}

func TestConfusionCountsCallsAgainstVerdicts(t *testing.T) {
	var c Confusion
	for _, x := range [][2]bool{{true, true}, {true, true}, {true, false}, {false, true}, {false, false}, {false, false}} {
		c.Add(x[0], x[1])
	}
	if c != (Confusion{TP: 2, FP: 1, FN: 1, TN: 2}) || c.Total() != 6 {
		t.Fatalf("%+v", c)
	}
	if p, ok := c.PrecisionValue(); !ok || p < 0.666 || p > 0.667 {
		t.Errorf("precision %v", p)
	}
	if c.Precision() != "0.667 (2/3)" || c.Recall() != "0.667 (2/3)" {
		t.Errorf("%s %s", c.Precision(), c.Recall())
	}
	var none Confusion
	if _, ok := none.PrecisionValue(); ok || none.Recall() != "n/a" {
		t.Error("no calls, no precision")
	}
}
