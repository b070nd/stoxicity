package eval

import "fmt"

// Confusion counts the lens's "relevant" calls (score at or above the
// threshold) against the user's verdicts.
type Confusion struct{ TP, FP, FN, TN int }

// Add counts one item: called says the lens called it relevant, kept that the
// user kept it.
func (c *Confusion) Add(called, kept bool) {
	switch {
	case called && kept:
		c.TP++
	case called:
		c.FP++
	case kept:
		c.FN++
	default:
		c.TN++
	}
}

// Total is the number of items counted.
func (c Confusion) Total() int { return c.TP + c.FP + c.FN + c.TN }

// Kept is the number of items the user kept.
func (c Confusion) Kept() int { return c.TP + c.FN }

// Calls is the number of items the lens called relevant.
func (c Confusion) Calls() int { return c.TP + c.FP }

// PrecisionValue is the share of the lens's relevant calls the user kept; false
// when the lens made none.
func (c Confusion) PrecisionValue() (float64, bool) {
	if c.Calls() == 0 {
		return 0, false
	}
	return float64(c.TP) / float64(c.Calls()), true
}

// RecallValue is the share of the kept items the lens called relevant; false
// when the user kept none.
func (c Confusion) RecallValue() (float64, bool) {
	if c.Kept() == 0 {
		return 0, false
	}
	return float64(c.TP) / float64(c.Kept()), true
}

// Ratio writes num over den as "0.667 (2/3)", or "n/a" if den is zero.
func Ratio(num, den int) string {
	if den == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.3f (%d/%d)", float64(num)/float64(den), num, den)
}

// Precision is PrecisionValue written with its counts.
func (c Confusion) Precision() string { return Ratio(c.TP, c.Calls()) }

// Recall is RecallValue written with its counts.
func (c Confusion) Recall() string { return Ratio(c.TP, c.Kept()) }
