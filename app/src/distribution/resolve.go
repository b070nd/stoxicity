package distribution

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// State of a payout once its observations are read together.
type State string

const (
	StateDeclared    State = "declared"
	StateEstimated   State = "estimated"
	StateCancelled   State = "cancelled"
	StateConflicting State = "conflicting" // sources disagree; nobody has chosen
)

// Payout is one payout as the observations, read together, say it is. Amount is
// per share. It is not cash and has no received state: see the package comment.
type Payout struct {
	Key       Key
	State     State
	Amount    money.Decimal
	Currency  money.Currency
	ExDay     string
	RecordDay string
	PayDay    string
	Sources   []string
	Reasons   []string
	// Observations are the newest of each source.
	Observations []Obs
	// Revisions is how many observations of this payout are stored in all.
	Revisions int
	// Adjusted says the amount was restated for a share split after the ex-date.
	Adjusted bool
}

// Resolve reads observations together. Only those received by knownAt count
// (zero: all). Each source's newest observation of a payout stands for it; then:
//
//   - declared observations that agree make a declared payout; a declared
//     payout beats estimates of it;
//   - disagreement on ex-date, amount, currency or another stated date is
//     conflicting, and so is a cancellation beside a declaration;
//   - a cancellation with no declaration cancels the payout and its estimates;
//   - estimates alone make an estimated payout, or a conflicting one when they
//     disagree.
//
// The result is ordered by ex-date.
func Resolve(all []Obs, knownAt time.Time) []Payout {
	groups := map[Key][]Obs{}
	for _, o := range all {
		if !knownAt.IsZero() && o.Received.After(knownAt) {
			continue
		}
		groups[o.Key()] = append(groups[o.Key()], o)
	}
	out := make([]Payout, 0, len(groups))
	for k, obs := range groups {
		out = append(out, resolveOne(k, obs))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExDay != out[j].ExDay {
			return out[i].ExDay < out[j].ExDay
		}
		return out[i].Key.String() < out[j].Key.String()
	})
	return out
}

func resolveOne(k Key, obs []Obs) Payout {
	p := Payout{Key: k, Revisions: len(obs)}
	latest := map[string]Obs{}
	for _, o := range obs {
		if cur, ok := latest[o.Source]; !ok || o.Seq > cur.Seq {
			latest[o.Source] = o
		}
	}
	var declared, estimated, cancelled []Obs
	for _, src := range sortedSources(latest) {
		o := latest[src]
		p.Observations = append(p.Observations, o)
		switch o.Status {
		case Declared:
			declared = append(declared, o)
		case Estimated:
			estimated = append(estimated, o)
		case Cancelled:
			cancelled = append(cancelled, o)
		}
	}
	take := func(set []Obs, st State) {
		first := set[0]
		p.State, p.Amount, p.Currency, p.ExDay = st, first.Amount, first.Currency, first.ExDay
		for _, o := range set {
			if p.RecordDay == "" {
				p.RecordDay = o.RecordDay
			}
			if p.PayDay == "" {
				p.PayDay = o.PayDay
			}
			p.Sources = append(p.Sources, o.Source)
		}
	}
	conflict := func(set []Obs, what string) {
		p.State = StateConflicting
		p.ExDay = set[0].ExDay
		for _, o := range set {
			p.Sources = append(p.Sources, o.Source)
			if o.ExDay < p.ExDay {
				p.ExDay = o.ExDay
			}
		}
		p.Reasons = append(p.Reasons, what)
	}
	switch {
	case len(declared) > 0 && len(cancelled) > 0:
		conflict(append(append([]Obs{}, declared...), cancelled...), fmt.Sprintf("%s declare it and %s cancelled it", names(declared), names(cancelled)))
	case len(declared) > 0:
		if why := disagreement(declared); why != "" {
			conflict(declared, why)
		} else {
			take(declared, StateDeclared)
			if len(estimated) > 0 {
				p.Reasons = append(p.Reasons, fmt.Sprintf("%d estimate(s) ignored: the payout is declared", len(estimated)))
			}
		}
	case len(cancelled) > 0:
		take(cancelled, StateCancelled)
		if len(estimated) > 0 {
			p.Reasons = append(p.Reasons, fmt.Sprintf("%d estimate(s) ignored: the payout is cancelled", len(estimated)))
		}
	default:
		if why := disagreement(estimated); why != "" {
			conflict(estimated, why)
		} else {
			take(estimated, StateEstimated)
			p.Reasons = append(p.Reasons, "an estimate: not declared by the issuer and not cash")
		}
	}
	return p
}

func sortedSources(m map[string]Obs) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func names(set []Obs) string {
	var n []string
	for _, o := range set {
		n = append(n, o.Source)
	}
	return strings.Join(n, ", ")
}

// disagreement names what differs among observations that should agree, or "".
// A date one source leaves empty does not disagree with a date another gives.
func disagreement(set []Obs) string {
	if len(set) < 2 {
		return ""
	}
	var parts []string
	check := func(name string, get func(Obs) string, skipEmpty bool) {
		vals := map[string][]string{}
		for _, o := range set {
			v := get(o)
			if v == "" && skipEmpty {
				continue
			}
			vals[v] = append(vals[v], o.Source)
		}
		if len(vals) > 1 {
			var ks []string
			for v, srcs := range vals {
				ks = append(ks, fmt.Sprintf("%s %s", strings.Join(srcs, "/"), v))
			}
			sort.Strings(ks)
			parts = append(parts, fmt.Sprintf("%s differs (%s)", name, strings.Join(ks, "; ")))
		}
	}
	check("ex-date", func(o Obs) string { return o.ExDay }, false)
	check("amount", func(o Obs) string { return o.Amount.String() }, false)
	check("currency", func(o Obs) string { return string(o.Currency) }, false)
	check("record date", func(o Obs) string { return o.RecordDay }, true)
	check("pay date", func(o Obs) string { return o.PayDay }, true)
	return strings.Join(parts, "; ")
}

// ---- share splits ----

// Split is a share split of one security: from the first Day at the new price,
// one share became To/From shares (2-for-1 is From 1, To 2).
type Split struct {
	SecurityID string
	Day        string
	From, To   int64
}

// Validate says why a split is unusable.
func (s Split) Validate() error {
	if s.SecurityID == "" || !validDay(s.Day) || s.From < 1 || s.To < 1 {
		return fmt.Errorf("distribution: split %+v needs a security, a day and positive From and To", s)
	}
	return nil
}

// AdjustForSplits restates payouts so amounts are comparable across splits: an
// amount whose ex-date is before a split's day is per old share, and is divided
// by the split's ratio. A payout declared after the split is untouched. The
// division is exact when it can be; otherwise it is rounded half-even at 8 more
// digits and the payout says so. Splits of other securities are ignored.
func AdjustForSplits(ps []Payout, splits []Split) ([]Payout, error) {
	out := make([]Payout, len(ps))
	for i, p := range ps {
		out[i] = p
		for _, s := range splits {
			if s.SecurityID != p.Key.SecurityID {
				continue
			}
			if err := s.Validate(); err != nil {
				return nil, err
			}
			if p.ExDay == "" || p.ExDay >= s.Day {
				continue
			}
			scaled, err := out[i].Amount.Mul(money.Int(s.From), uint8(out[i].Amount.Scale()), money.Exact)
			if err != nil {
				return nil, err
			}
			scale := uint8(min(18, out[i].Amount.Scale()+8))
			amt, err := scaled.Div(money.Int(s.To), scale, money.Exact)
			if err != nil {
				if amt, err = scaled.Div(money.Int(s.To), scale, money.HalfEven); err != nil {
					return nil, err
				}
				out[i].Reasons = append(out[i].Reasons, fmt.Sprintf("restated for the %d-for-%d split of %s; rounded half-even", s.To, s.From, s.Day))
			} else {
				out[i].Reasons = append(out[i].Reasons, fmt.Sprintf("restated for the %d-for-%d split of %s", s.To, s.From, s.Day))
			}
			out[i].Amount, out[i].Adjusted = trim(amt), true
		}
	}
	return out, nil
}

// trim drops trailing zeros of the fraction so restated amounts read plainly,
// keeping at least two digits.
func trim(d money.Decimal) money.Decimal {
	for d.Scale() > 2 {
		r, err := d.Rescale(uint8(d.Scale()-1), money.Exact)
		if err != nil {
			break
		}
		d = r
	}
	return d
}
