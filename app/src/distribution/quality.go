package distribution

import (
	"fmt"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
)

// Coverage says whether the absence of payouts means anything.
type Coverage string

const (
	// Observed: at least one payout (not only cancelled ones) is on record.
	Observed Coverage = "observed"
	// NoneByDesign: the instrument record says the class accumulates and pays no
	// cash distribution. That is a fact about the instrument, not about the data.
	NoneByDesign Coverage = "none_by_design"
	// Unknown: nothing is on record and nothing says there should not be. Missing
	// data is not "pays no dividend".
	Unknown Coverage = "unknown"
)

// CoverState says whether the free-cash-flow cover could be worked out.
type CoverState string

const (
	CoverComputed    CoverState = "computed"
	CoverUnsupported CoverState = "unsupported_sector"
	CoverUnknown     CoverState = "unknown_inputs"
)

// unsupportedSectors are kinds of business for which payout against free cash
// flow says nothing: their cash flow is the business itself (lending, float,
// property income, distributions mandated from income) or the payout comes from
// holdings, not a company's cash flow.
var unsupportedSectors = map[string]string{
	"bank":      "a bank's cash flow includes deposits and lending, so free cash flow is not meaningful",
	"insurance": "an insurer's cash flow includes policyholder float, so free cash flow is not meaningful",
	"financial": "financial companies' cash flow includes balances held for others",
	"reit":      "a REIT distributes mandated income; funds from operations, not free cash flow, is the measure",
	"bdc":       "a business development company distributes investment income",
	"mlp":       "a partnership distributes available cash by design",
}

// SectorSupport says whether free-cash-flow cover applies to the sector label.
// ok is false for an unsupported sector (why says why); an empty or unrecognised
// label is not supported either, because it is not known to be.
func SectorSupport(label string) (ok bool, why string) {
	l := strings.ToLower(strings.TrimSpace(label))
	if l == "" {
		return false, "the sector is not known"
	}
	for key, w := range unsupportedSectors {
		if strings.Contains(l, key) {
			return false, w
		}
	}
	if l == "operating company" || l == "industrial" || l == "technology" || l == "consumer" || l == "energy" || l == "utility" || l == "healthcare" || l == "materials" {
		return true, ""
	}
	return false, fmt.Sprintf("the sector %q is not one this check has been told how to treat", label)
}

// Input is what quality is computed from. Payouts must be resolved and, where
// splits are known, restated for them (AdjustForSplits).
type Input struct {
	Security instrument.Security
	// Day is the as-of day, YYYY-MM-DD.
	Day     string
	Payouts []Payout
	// SplitsKnown says the caller supplied the split history. When false, a
	// "cut" may be a split.
	SplitsKnown bool
	// Sector is a label the user supplies; empty is unknown.
	Sector string
	// FCFPerShare is trailing free cash flow per share in FCFCurrency, when the
	// user supplies it.
	FCFPerShare *money.Decimal
	FCFCurrency money.Currency
}

// Change compares a payout with the same period one year earlier.
type Change struct {
	Period    string
	Prior     string
	Amount    money.Decimal
	PriorAmt  money.Decimal
	Currency  money.Currency
	Direction string        // increase, flat or cut
	Ratio     money.Decimal // amount / prior - 1, four digits
}

// Quality describes a security's distribution history. Nothing in it is a
// forecast.
type Quality struct {
	Security  string
	Day       string
	Coverage  Coverage
	Frequency string
	// OrdinaryTTM and SpecialTTM are per-share sums of declared payouts with an
	// ex-date in the 12 months to Day, per currency. Estimates, cancelled and
	// conflicting payouts are not in them; other types are not in either.
	OrdinaryTTM map[money.Currency]money.Decimal
	SpecialTTM  map[money.Currency]money.Decimal
	// Latest is the newest declared ordinary payout compared with its prior-year
	// period; nil when there is none or no comparable one.
	Latest *Change
	// Cuts are the declared ordinary payouts in the 24 months to Day below their
	// prior-year period.
	Cuts          []Change
	Cancellations int
	// MissedPeriods are periods with no payout of any status between the first and
	// last observed period of the frequency.
	MissedPeriods []string
	HistoryMonths int
	Cover         CoverState
	CoverRatio    *money.Decimal
	CoverWhy      string
	Estimated     []Payout // estimates, listed apart; an overdue one is also in Unknowns
	Unknowns      []string
}

// Analyse computes quality. It never fills a gap: what it cannot know is listed
// in Unknowns.
func Analyse(in Input) Quality {
	q := Quality{Security: in.Security.ID, Day: in.Day,
		OrdinaryTTM: map[money.Currency]money.Decimal{}, SpecialTTM: map[money.Currency]money.Decimal{}}
	windowStart := addDays(in.Day, -365)
	cutStart := addDays(in.Day, -730)

	var own []Payout
	for _, p := range in.Payouts {
		if p.Key.SecurityID != in.Security.ID {
			continue
		}
		if p.ExDay <= in.Day || p.State == StateEstimated {
			own = append(own, p)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].ExDay < own[j].ExDay })

	nonCancelled := 0
	for _, p := range own {
		if p.State != StateCancelled {
			nonCancelled++
		}
	}
	switch {
	case in.Security.Distribution == instrument.Accumulating:
		q.Coverage = NoneByDesign
		if nonCancelled > 0 {
			q.Unknowns = append(q.Unknowns, "payouts are on record for a class the instrument record says accumulates: one of the two is wrong")
		}
	case nonCancelled > 0:
		q.Coverage = Observed
	default:
		q.Coverage = Unknown
		q.Unknowns = append(q.Unknowns, "no payout is on record: that is not evidence the security pays none")
	}

	byPeriod := map[string]Payout{}
	disputed := map[string]bool{} // ordinary periods on record but in conflict: present, not missed
	var ordinary []Payout
	kinds := map[periodKind]bool{}
	for _, p := range own {
		switch p.State {
		case StateCancelled:
			if p.ExDay >= cutStart {
				q.Cancellations++
			}
		case StateEstimated:
			q.Estimated = append(q.Estimated, p)
			if p.ExDay <= in.Day {
				q.Unknowns = append(q.Unknowns, fmt.Sprintf("%s was estimated for %s and has not been declared since: overdue, not a payout", p.Key, p.ExDay))
			}
			continue
		case StateConflicting:
			q.Unknowns = append(q.Unknowns, fmt.Sprintf("%s is conflicting between sources and counts for nothing", p.Key))
			disputed[p.Key.Period] = p.Key.Type == Ordinary
			continue
		case StateDeclared:
			if p.ExDay >= windowStart && p.ExDay <= in.Day {
				switch p.Key.Type {
				case Ordinary:
					q.OrdinaryTTM[p.Currency] = addTo(q.OrdinaryTTM[p.Currency], p.Amount)
				case Special:
					q.SpecialTTM[p.Currency] = addTo(q.SpecialTTM[p.Currency], p.Amount)
				}
			}
			if p.Key.Type == TypeUnknown {
				q.Unknowns = append(q.Unknowns, fmt.Sprintf("%s has no type and is left out of the run-rate", p.Key))
			}
		}
		if p.Key.Type == Ordinary {
			if _, _, k, ok := parsePeriod(p.Key.Period); ok {
				kinds[k] = true
			}
			byPeriod[p.Key.Period] = p
			if p.State == StateDeclared {
				ordinary = append(ordinary, p)
			}
		}
	}

	// Frequency is what the sources say the periods are, not guessed from dates.
	switch len(kinds) {
	case 0:
		q.Frequency = "unknown"
	case 1:
		for k := range kinds {
			q.Frequency = k.String()
		}
	default:
		q.Frequency = "irregular"
		q.Unknowns = append(q.Unknowns, "ordinary payouts are stated in more than one period granularity")
	}

	// Gaps between the first and last period on record.
	if len(byPeriod) >= 2 && len(kinds) == 1 {
		var periods []string
		for p := range byPeriod {
			periods = append(periods, p)
		}
		present := func(p string) bool { _, ok := byPeriod[p]; return ok || disputed[p] }
		sort.Slice(periods, func(i, j int) bool { return periodOrder(periods[i]) < periodOrder(periods[j]) })
		for p := nextPeriod(periods[0]); p != "" && periodOrder(p) < periodOrder(periods[len(periods)-1]); p = nextPeriod(p) {
			if !present(p) {
				q.MissedPeriods = append(q.MissedPeriods, p)
			}
		}
	}

	// Changes against the same period a year earlier.
	var latest *Payout
	for i := range ordinary {
		if latest == nil || ordinary[i].ExDay > latest.ExDay {
			latest = &ordinary[i]
		}
	}
	change := func(p Payout) (Change, bool) {
		prior, ok := byPeriod[PriorYear(p.Key.Period)]
		if !ok || prior.State != StateDeclared || prior.Currency != p.Currency {
			return Change{}, false
		}
		c := Change{Period: p.Key.Period, Prior: prior.Key.Period, Amount: p.Amount, PriorAmt: prior.Amount, Currency: p.Currency}
		switch cmp := p.Amount.Cmp(prior.Amount); {
		case cmp < 0:
			c.Direction = "cut"
		case cmp > 0:
			c.Direction = "increase"
		default:
			c.Direction = "flat"
		}
		if r, err := p.Amount.Div(prior.Amount, 4, money.HalfEven); err == nil {
			if r, err = r.Sub(money.Int(1)); err == nil {
				c.Ratio = r
			}
		}
		return c, true
	}
	if latest != nil {
		if c, ok := change(*latest); ok {
			q.Latest = &c
		} else {
			q.Unknowns = append(q.Unknowns, fmt.Sprintf("no declared payout for %s in the same currency to compare %s with", PriorYear(latest.Key.Period), latest.Key.Period))
		}
	}
	for _, p := range ordinary {
		if p.ExDay < cutStart {
			continue
		}
		if c, ok := change(p); ok && c.Direction == "cut" {
			q.Cuts = append(q.Cuts, c)
		}
	}
	if !in.SplitsKnown && len(q.Cuts) > 0 {
		q.Unknowns = append(q.Unknowns, "no split history was supplied: a cut may be a share split")
	}
	if !in.SplitsKnown && len(ordinary) > 0 {
		q.Unknowns = append(q.Unknowns, "split history not supplied: amounts are as stated, per share at the time")
	}

	if len(own) >= 2 {
		if a, b := own[0].ExDay, own[len(own)-1].ExDay; a != "" && b != "" {
			if y1, m1, ok1 := ym(a); ok1 {
				if y2, m2, ok2 := ym(b); ok2 {
					q.HistoryMonths = (y2-y1)*12 + (m2 - m1)
				}
			}
		}
	}

	q.cover(in)
	return q
}

func ym(day string) (int, int, bool) {
	var y, m, d int
	if n, _ := fmt.Sscanf(day, "%d-%d-%d", &y, &m, &d); n != 3 {
		return 0, 0, false
	}
	return y, m, true
}

func periodOrder(p string) int {
	y, i, k, ok := parsePeriod(p)
	if !ok {
		return 0
	}
	switch k {
	case monthly:
		return y*12 + i
	case quarterly:
		return y*12 + (i-1)*3 + 1
	case halfYearly:
		return y*12 + (i-1)*6 + 1
	}
	return y*12 + 1
}

func addTo(a, b money.Decimal) money.Decimal {
	s, err := a.Add(b)
	if err != nil {
		return a
	}
	return s
}

// cover works out free-cash-flow cover: trailing FCF per share over the ordinary
// payout per share of the trailing year, in one currency.
func (q *Quality) cover(in Input) {
	if in.Security.Kind != instrument.KindStock {
		q.Cover, q.CoverWhy = CoverUnsupported, "free cash flow cover applies to a company's shares; a fund pays out what its holdings pay"
		return
	}
	if ok, why := SectorSupport(in.Sector); !ok {
		if strings.TrimSpace(in.Sector) == "" {
			q.Cover, q.CoverWhy = CoverUnknown, why
			q.Unknowns = append(q.Unknowns, "sector not supplied: cover cannot be judged")
		} else {
			q.Cover, q.CoverWhy = CoverUnsupported, why
		}
		return
	}
	if in.FCFPerShare == nil || !in.FCFCurrency.Valid() {
		q.Cover, q.CoverWhy = CoverUnknown, "free cash flow per share and its currency were not supplied"
		q.Unknowns = append(q.Unknowns, "free cash flow per share not supplied")
		return
	}
	paid, ok := q.OrdinaryTTM[in.FCFCurrency]
	if len(q.OrdinaryTTM) > 1 || !ok && len(q.OrdinaryTTM) == 1 {
		q.Cover, q.CoverWhy = CoverUnknown, fmt.Sprintf("the payout is not in %s; convert with a stated rate first", in.FCFCurrency)
		return
	}
	if !ok || paid.Sign() == 0 {
		q.Cover, q.CoverWhy = CoverUnknown, "no declared ordinary payout in the last 12 months to compare with"
		return
	}
	r, err := in.FCFPerShare.Div(paid, 2, money.HalfEven)
	if err != nil {
		q.Cover, q.CoverWhy = CoverUnknown, err.Error()
		return
	}
	q.Cover, q.CoverRatio = CoverComputed, &r
	q.CoverWhy = "trailing free cash flow per share divided by the declared ordinary payout of the last 12 months"
}
