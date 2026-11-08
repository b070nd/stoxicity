package quotes

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
)

// Verdict is whether a value may be used.
type Verdict string

const (
	OK            Verdict = "ok"
	Missing       Verdict = "missing"
	Stale         Verdict = "stale"
	Conflicting   Verdict = "conflicting"
	UnitAmbiguous Verdict = "unit_ambiguous"
)

// InverseScale is the number of digits an inverted rate keeps; it is rounded
// half-even, and the answer says it was inverted.
const InverseScale = 12

// Need says what an action requires of a value.
type Need struct {
	// Day is the day the value is wanted for, YYYY-MM-DD.
	Day string
	// KnownAt limits the answer to what this system had received by then; zero
	// means everything stored.
	KnownAt time.Time
	// MaxAgeDays is how many calendar days older than Day the value may be. 0
	// means it must be dated Day. It is the caller's decision: weekends and
	// holidays are not guessed.
	MaxAgeDays int

	// Basis is the kind of rate wanted. A fill is never returned for a reference
	// request or the other way round. Default: reference.
	Basis money.Basis

	// Kind and Adjustment select the price. Defaults: close, raw.
	Kind       Kind
	Adjustment Adjustment
	// RequireStatedUnit refuses a price whose unit came from the instrument record
	// rather than the source. Actions that size money set it.
	RequireStatedUnit bool
}

// FXAnswer is the outcome of asking for a rate.
type FXAnswer struct {
	Verdict  Verdict
	Rate     *money.Rate
	Day      string // the day of the rate used
	AgeDays  int
	Inverted bool
	Sources  []string
	Reasons  []string
	// Candidates are the observations behind a conflict.
	Candidates []FXObs
}

func (n Need) fxBasis() money.Basis {
	if n.Basis == "" {
		return money.BasisReference
	}
	return n.Basis
}

// ResolveFX answers "what is the rate base/quote for need.Day" from observations.
// It uses the direct pair, or the inverse pair when only that exists (said in the
// answer); it never derives a cross rate through a third currency.
func ResolveFX(all []FXObs, base, quote money.Currency, need Need) FXAnswer {
	if !validDay(need.Day) {
		return FXAnswer{Verdict: Missing, Reasons: []string{fmt.Sprintf("the day %q is not YYYY-MM-DD", need.Day)}}
	}
	basis := need.fxBasis()
	var direct, inverse []FXObs
	otherBasis, future, unseen := 0, 0, 0
	for _, o := range all {
		isDirect := o.Base == base && o.Quote == quote
		isInverse := o.Base == quote && o.Quote == base
		if !isDirect && !isInverse {
			continue
		}
		if o.Basis != basis {
			otherBasis++
			continue
		}
		if !need.KnownAt.IsZero() && o.Received.After(need.KnownAt) {
			unseen++
			continue
		}
		if o.Day > need.Day {
			future++
			continue
		}
		if isDirect {
			direct = append(direct, o)
		} else {
			inverse = append(inverse, o)
		}
	}
	use, inverted := direct, false
	if len(use) == 0 {
		use, inverted = inverse, true
	}
	if len(use) == 0 {
		why := []string{fmt.Sprintf("no %s rate %s/%s dated %s or earlier is stored", basis, base, quote, need.Day)}
		if otherBasis > 0 {
			why = append(why, fmt.Sprintf("%d observation(s) of another basis exist and are not substituted", otherBasis))
		}
		if unseen > 0 {
			why = append(why, fmt.Sprintf("%d observation(s) were received after %s", unseen, need.KnownAt.UTC().Format(time.RFC3339)))
		}
		if future > 0 {
			why = append(why, fmt.Sprintf("%d observation(s) are dated after %s", future, need.Day))
		}
		return FXAnswer{Verdict: Missing, Reasons: why}
	}

	newest := ""
	for _, o := range use {
		if o.Day > newest {
			newest = o.Day
		}
	}
	age, _ := DaysBetween(newest, need.Day)
	ans := FXAnswer{Day: newest, AgeDays: age, Inverted: inverted}
	if age > need.MaxAgeDays {
		ans.Verdict = Stale
		ans.Reasons = []string{fmt.Sprintf("the newest %s rate is dated %s, %d day(s) before %s; at most %d allowed", basis, newest, age, need.Day, need.MaxAgeDays)}
		return ans
	}

	// The newest revision of each source for that day.
	latest := map[string]FXObs{}
	for _, o := range use {
		if o.Day != newest {
			continue
		}
		if cur, ok := latest[o.Source]; !ok || o.Seq > cur.Seq {
			latest[o.Source] = o
		}
	}
	values := map[string][]string{}
	var rates []money.Rate
	for _, src := range sortedKeys(latest) {
		o := latest[src]
		r, err := o.Rate()
		if err != nil {
			ans.Verdict = Conflicting
			ans.Reasons = []string{err.Error()}
			return ans
		}
		if inverted {
			if r, err = r.Invert(InverseScale, money.HalfEven); err != nil {
				ans.Verdict = Conflicting
				ans.Reasons = []string{err.Error()}
				return ans
			}
		}
		key := r.Value.String()
		values[key] = append(values[key], src)
		rates = append(rates, r)
		ans.Candidates = append(ans.Candidates, o)
	}
	if len(values) > 1 {
		ans.Verdict = Conflicting
		keys := sortedKeys(values)
		for _, k := range keys {
			ans.Reasons = append(ans.Reasons, fmt.Sprintf("%s says %s/%s = %s on %s", strings.Join(values[k], ", "), base, quote, k, newest))
		}
		return ans
	}
	ans.Verdict = OK
	r := rates[0]
	ans.Rate = &r
	ans.Sources = sortedKeys(latest)
	if inverted {
		ans.Reasons = append(ans.Reasons, fmt.Sprintf("inverted from %s/%s to %d digits, rounded half-even", quote, base, InverseScale))
	}
	if basis == money.BasisReference {
		ans.Reasons = append(ans.Reasons, "a reference rate is for reporting; it is not a rate anything was or can be traded at")
	}
	return ans
}

// PriceAnswer is the outcome of asking for a price.
type PriceAnswer struct {
	Verdict    Verdict
	Price      *money.Price
	Day        string
	AgeDays    int
	Delay      Delay
	UnitBasis  UnitBasis
	Sources    []string
	Reasons    []string
	Candidates []PriceObs
}

func (n Need) kind() Kind {
	if n.Kind == "" {
		return Close
	}
	return n.Kind
}

func (n Need) adjustment() Adjustment {
	if n.Adjustment == "" {
		return Raw
	}
	return n.Adjustment
}

// ResolvePrice answers "what is the price of this listing for need.Day". The
// listing's quote unit is what a price must be in: a price stated in GBP for a
// listing quoted in GBp is refused, not converted, because that mismatch is the
// classic factor-of-100 error and the file is wrong in one of the two places.
func ResolvePrice(all []PriceObs, l instrument.Listing, need Need) PriceAnswer {
	if !validDay(need.Day) {
		return PriceAnswer{Verdict: Missing, Reasons: []string{fmt.Sprintf("the day %q is not YYYY-MM-DD", need.Day)}}
	}
	kind, adj := need.kind(), need.adjustment()
	var use []PriceObs
	otherAdj, future, unseen := 0, 0, 0
	for _, o := range all {
		if o.ListingID != l.ID || o.Kind != kind {
			continue
		}
		if o.Adjustment != adj {
			otherAdj++
			continue
		}
		if !need.KnownAt.IsZero() && o.Received.After(need.KnownAt) {
			unseen++
			continue
		}
		if o.Day > need.Day {
			future++
			continue
		}
		use = append(use, o)
	}
	if len(use) == 0 {
		why := []string{fmt.Sprintf("no %s %s price of %s dated %s or earlier is stored", adj, kind, l.ID, need.Day)}
		if otherAdj > 0 {
			why = append(why, fmt.Sprintf("%d observation(s) with another adjustment exist and are not substituted", otherAdj))
		}
		if unseen > 0 {
			why = append(why, fmt.Sprintf("%d observation(s) were received after %s", unseen, need.KnownAt.UTC().Format(time.RFC3339)))
		}
		if future > 0 {
			why = append(why, fmt.Sprintf("%d observation(s) are dated after %s", future, need.Day))
		}
		return PriceAnswer{Verdict: Missing, Reasons: why}
	}
	newest := ""
	for _, o := range use {
		if o.Day > newest {
			newest = o.Day
		}
	}
	age, _ := DaysBetween(newest, need.Day)
	ans := PriceAnswer{Day: newest, AgeDays: age}
	if age > need.MaxAgeDays {
		ans.Verdict = Stale
		ans.Reasons = []string{fmt.Sprintf("the newest price is dated %s, %d day(s) before %s; at most %d allowed", newest, age, need.Day, need.MaxAgeDays)}
		return ans
	}
	latest := map[string]PriceObs{}
	for _, o := range use {
		if o.Day != newest {
			continue
		}
		if cur, ok := latest[o.Source]; !ok || o.Seq > cur.Seq {
			latest[o.Source] = o
		}
	}
	values := map[string][]string{}
	for _, src := range sortedKeys(latest) {
		o := latest[src]
		ans.Candidates = append(ans.Candidates, o)
		if o.Price.Unit.Code != l.QuoteUnit {
			ans.Reasons = append(ans.Reasons, fmt.Sprintf("%s states %s but the listing %s is quoted in %s", src, o.Price.Unit.Code, l.ID, l.QuoteUnit))
		}
		if need.RequireStatedUnit && o.UnitBasis != UnitStated {
			ans.Reasons = append(ans.Reasons, fmt.Sprintf("%s did not state the unit; %s was taken from the instrument record", src, l.QuoteUnit))
		}
		key := o.Price.Value.String() + " " + o.Price.Unit.Code
		values[key] = append(values[key], src)
	}
	if len(ans.Reasons) > 0 {
		ans.Verdict = UnitAmbiguous
		return ans
	}
	if len(values) > 1 {
		ans.Verdict = Conflicting
		for _, k := range sortedKeys(values) {
			ans.Reasons = append(ans.Reasons, fmt.Sprintf("%s says %s on %s", strings.Join(values[k], ", "), k, newest))
		}
		return ans
	}
	first := latest[sortedKeys(latest)[0]]
	p := first.Price
	ans.Verdict, ans.Price, ans.Delay, ans.UnitBasis = OK, &p, first.Delay, first.UnitBasis
	ans.Sources = sortedKeys(latest)
	if first.UnitBasis == UnitFromListing {
		ans.Reasons = append(ans.Reasons, "the source did not state the unit; it is the listing's")
	}
	return ans
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
