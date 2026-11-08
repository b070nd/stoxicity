// Package paper is the local paper engine: it decides what daily price bars
// would have done to an order that was acknowledged, and says so as ordinary
// order reports, so a paper book uses the same intent, fill and ledger contracts
// as any other book. It needs no broker.
//
// Daily bars cannot say what happened inside a day, so the rules lean against
// the order, and a fill is never guaranteed:
//
//   - An order is eligible only in the first bar after the day it was
//     acknowledged. Nothing fills in the bar it was sent in.
//   - It is a DAY order: whatever has not filled when that bar ends expires.
//   - A buy fills only if the bar's low went strictly below the limit, a sell
//     only if the high went strictly above it. Touching the limit is ambiguous
//     (the order may have been behind others in the queue) and does not fill.
//   - The price is the limit, never better: no price improvement at a gap.
//   - The quantity is bounded by a share of the day's volume, and by a seeded
//     fraction of that, so repeated runs give the same answer and different
//     seeds give different, equally defensible, partial fills.
//   - A bar that cannot be right (high below low, a price outside the range,
//     a repeated day) is refused, not simulated.
package paper

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
)

// Bar is one day of trading in a security, as traded (not adjusted).
type Bar struct {
	Security               string
	Day                    string
	Open, High, Low, Close money.Decimal
	Volume                 int64
	Currency               money.Currency
}

// Validate refuses a bar that cannot be right.
func (b Bar) Validate() error {
	if _, err := time.Parse(time.DateOnly, b.Day); err != nil {
		return fmt.Errorf("paper: bar day %q is not YYYY-MM-DD", b.Day)
	}
	for _, p := range []money.Decimal{b.Open, b.High, b.Low, b.Close} {
		if p.Sign() <= 0 {
			return fmt.Errorf("paper: %s %s has a price that is not positive", b.Security, b.Day)
		}
	}
	if b.High.Cmp(b.Low) < 0 {
		return fmt.Errorf("paper: %s %s has a high below its low", b.Security, b.Day)
	}
	for name, p := range map[string]money.Decimal{"open": b.Open, "close": b.Close} {
		if p.Cmp(b.Low) < 0 || p.Cmp(b.High) > 0 {
			return fmt.Errorf("paper: %s %s has its %s outside the day's range", b.Security, b.Day, name)
		}
	}
	if b.Volume < 0 {
		return fmt.Errorf("paper: %s %s has a negative volume", b.Security, b.Day)
	}
	return nil
}

// Config fixes how the engine behaves. It is stored with the book on first use
// and cannot change afterwards, so a book's fills are reproducible.
type Config struct {
	Seed int64 `json:"seed"`
	// Participation is the most of a day's volume the order may take.
	Participation money.Decimal `json:"participation"`
	// MinFill is the lowest fraction of that capacity the seeded draw gives.
	MinFill money.Decimal `json:"min_fill"`
	// SettleDays is the number of weekdays after the fill when cash settles.
	SettleDays int `json:"settle_days"`
}

// DefaultConfig is cautious: one percent of the volume, at least a quarter of it.
func DefaultConfig() Config {
	return Config{Seed: 1, Participation: money.MustDecimal("0.01"), MinFill: money.MustDecimal("0.25"), SettleDays: 2}
}

// Validate checks the configuration.
func (c Config) Validate() error {
	one := money.Int(1)
	if c.Participation.Sign() <= 0 || c.Participation.Cmp(one) > 0 {
		return errors.New("paper: participation must be above 0 and at most 1")
	}
	if c.MinFill.Sign() < 0 || c.MinFill.Cmp(one) > 0 {
		return errors.New("paper: min-fill must be between 0 and 1")
	}
	if c.SettleDays < 0 || c.SettleDays > 10 {
		return errors.New("paper: settle-days must be between 0 and 10")
	}
	return nil
}

// Outcome is one thing the engine concluded about one order.
type Outcome struct {
	Order  string
	Day    string
	Report order.Reported
	Note   string
}

// Pending says an order could not be decided yet, and why.
type Pending struct {
	Order string
	Why   string
}

// Result is what a simulation produced.
type Result struct {
	Outcomes []Outcome
	Pending  []Pending
}

// draw is a number in [0, 1) that depends only on the seed, the order and the day.
func draw(seed int64, id, day string) float64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s", seed, id, day)))
	return float64(binary.BigEndian.Uint64(sum[:8])>>11) / float64(1<<53)
}

// AddWeekdays moves a day forward by n weekdays (weekends only; holidays are not known).
func AddWeekdays(day string, n int) (string, error) {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return "", err
	}
	for n > 0 {
		d = d.AddDate(0, 0, 1)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n--
		}
	}
	return d.Format(time.DateOnly), nil
}

// FeeFunc gives the fee in minor units of a fill of the given notional.
type FeeFunc func(v order.View, notional int64, qty money.Decimal) int64

// Simulate decides what the bars do to every order in views that is acknowledged
// and not closed. through is the last day whose bar may be used (a bar is a
// finished day, so it must not be later than the day before today). The
// outcomes carry deterministic keys: running the same bars again changes nothing.
func Simulate(cfg Config, views []order.View, bars []Bar, through string, fee FeeFunc) (Result, error) {
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	by := map[string][]Bar{}
	seen := map[string]bool{}
	for _, b := range bars {
		if err := b.Validate(); err != nil {
			return Result{}, err
		}
		k := b.Security + "|" + b.Day
		if seen[k] {
			return Result{}, fmt.Errorf("paper: %s has two bars for %s", b.Security, b.Day)
		}
		seen[k] = true
		by[b.Security] = append(by[b.Security], b)
	}
	for _, bs := range by {
		sort.Slice(bs, func(i, j int) bool { return bs[i].Day < bs[j].Day })
	}
	var res Result
	for _, v := range views {
		if v.State != order.Open && v.State != order.PartiallyFilled && v.State != order.CancelRequested {
			continue
		}
		if v.AckedAt.IsZero() {
			res.Pending = append(res.Pending, Pending{v.ID, "not acknowledged yet: a fill that arrived before the acknowledgement is not simulated"})
			continue
		}
		ack := v.AckedAt.UTC().Format(time.DateOnly)
		var bar *Bar
		for i := range by[v.Terms.Security] {
			if b := by[v.Terms.Security][i]; b.Day > ack {
				bar = &by[v.Terms.Security][i]
				break
			}
		}
		if bar == nil || bar.Day > through {
			res.Pending = append(res.Pending, Pending{v.ID, fmt.Sprintf("no finished bar after %s for %s up to %s", ack, v.Terms.Security, through)})
			continue
		}
		if a, _ := time.Parse(time.DateOnly, ack); a.AddDate(0, 0, 5).Before(mustDay(bar.Day)) {
			res.Pending = append(res.Pending, Pending{v.ID, fmt.Sprintf("the next bar in the file is %s, more than five days after %s: a missing day cannot be told from a holiday, so nothing is decided", bar.Day, ack)})
			continue
		}
		if bar.Currency != "" && bar.Currency != v.Terms.Currency {
			return Result{}, fmt.Errorf("paper: %s %s is in %s but the order is in %s", bar.Security, bar.Day, bar.Currency, v.Terms.Currency)
		}
		out, err := session(cfg, v, *bar, fee)
		if err != nil {
			return Result{}, err
		}
		res.Outcomes = append(res.Outcomes, out...)
	}
	return res, nil
}

func mustDay(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

// session runs one order through its one session.
func session(cfg Config, v order.View, b Bar, fee FeeFunc) ([]Outcome, error) {
	t := v.Terms
	key := func(suffix string) string { return fmt.Sprintf("paper:%s:%s%s", v.ID, b.Day, suffix) }
	mk := func(r order.Reported, note string) Outcome {
		return Outcome{Order: v.ID, Day: b.Day, Report: r, Note: note}
	}

	// a cancel asked for before the session starts is honoured; one asked for during
	// or after it races the session, and the session goes ahead
	if !v.CancelAt.IsZero() && v.CancelAt.UTC().Format(time.DateOnly) < b.Day {
		return []Outcome{mk(order.Reported{Kind: order.CancelledEv, Key: key(":cancelled")}, "cancel requested before the session")}, nil
	}
	var out []Outcome
	crossed := (t.Side == order.Buy && b.Low.Cmp(t.Limit) < 0) || (t.Side == order.Sell && b.High.Cmp(t.Limit) > 0)
	touched := (t.Side == order.Buy && b.Low.Cmp(t.Limit) == 0) || (t.Side == order.Sell && b.High.Cmp(t.Limit) == 0)
	var qty money.Decimal
	note := ""
	switch {
	case !crossed && touched:
		note = "the day only touched the limit: not a fill"
	case !crossed:
		note = "the price never reached the limit"
	case b.Volume == 0:
		note = "no volume"
	default:
		capacity, err := money.Int(b.Volume).Mul(cfg.Participation, 4, money.Down)
		if err != nil {
			return nil, err
		}
		frac := cfg.MinFill
		u, err := money.NewDecimal(int64(draw(cfg.Seed, v.ID, b.Day)*1e6), 6)
		if err != nil {
			return nil, err
		}
		span, _ := money.Int(1).Sub(cfg.MinFill)
		part, err := span.Mul(u, 6, money.Down)
		if err != nil {
			return nil, err
		}
		if frac, err = frac.Add(part); err != nil {
			return nil, err
		}
		cap2, err := capacity.Mul(frac, 4, money.Down)
		if err != nil {
			return nil, err
		}
		if !t.Fractional {
			if cap2, err = cap2.Rescale(0, money.Down); err != nil {
				return nil, err
			}
		}
		qty = cap2
		if qty.Cmp(v.Remaining) > 0 {
			qty = v.Remaining
		}
		if qty.Sign() <= 0 {
			note = "the day's volume leaves less than one share for this order"
		}
	}
	if qty.Sign() > 0 {
		e, err := t.Currency.Exponent()
		if err != nil {
			return nil, err
		}
		d, err := t.Limit.Mul(qty, uint8(e), money.HalfEven)
		if err != nil {
			return nil, err
		}
		g, err := money.NewAmount(d, t.Currency, money.HalfEven)
		if err != nil {
			return nil, err
		}
		f := order.Fill{ID: key(""), Qty: qty, Price: t.Limit, Gross: g.Minor(), Fee: fee(v, g.Minor(), qty), At: mustDay(b.Day).Add(16 * time.Hour)}
		if cfg.SettleDays > 0 {
			if f.SettleDay, err = AddWeekdays(b.Day, cfg.SettleDays); err != nil {
				return nil, err
			}
		}
		out = append(out, mk(order.Reported{Kind: order.FillEv, Fill: &f}, fmt.Sprintf("%s of %s at the limit %s", qty, t.Qty, t.Limit)))
	}
	left, err := v.Remaining.Sub(qty)
	if err != nil {
		return nil, err
	}
	if left.Sign() > 0 {
		// a DAY order that is not completely filled by the end of its session expires
		if !v.CancelAt.IsZero() {
			out = append(out, mk(order.Reported{Kind: order.CancelledEv, Key: key(":cancelled")}, "cancel requested during the session; the rest was cancelled"))
		} else {
			out = append(out, mk(order.Reported{Kind: order.ExpiredEv, Key: key(":expired")}, orDefault(note, "the rest of the DAY order expired")))
		}
	}
	return out, nil
}

func orDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
