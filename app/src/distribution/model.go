// Package distribution holds dividend and fund-distribution observations, exact
// and append-only, and decides what they mean: which payouts are declared, which
// are only estimated, which were cancelled and which sources disagree, and how
// the history looks (frequency, cuts, gaps, cover). Nothing here is cash. A
// declared payout is a company's promise per share, an estimate is a guess, and
// cash exists only when the ledger records it arriving (a later story); no
// function in this package turns the one into the other.
package distribution

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Type of payout. A source that does not say is `unknown`, never ordinary: an
// unclassified payout is kept out of the run-rate until someone classifies it.
type Type string

const (
	Ordinary        Type = "ordinary"
	Special         Type = "special"
	ReturnOfCapital Type = "return_of_capital"
	TypeUnknown     Type = "unknown"
)

// Status of an observation.
type Status string

const (
	Declared  Status = "declared"  // the issuer announced it
	Estimated Status = "estimated" // a forecast; may be wrong or never happen
	Cancelled Status = "cancelled" // the issuer withdrew a payout
)

// Key identifies a payout: one class of one security, one type, one period.
type Key struct {
	SecurityID string
	Type       Type
	// Period is the period the payout is for: 2026 (a year), 2026H1, 2026Q3 or
	// 2026-11. It is how a payout is compared with the same one a year earlier.
	Period string
}

func (k Key) String() string { return fmt.Sprintf("%s %s %s", k.SecurityID, k.Type, k.Period) }

// Obs is one observation of a payout by one source.
type Obs struct {
	Seq        int64
	SecurityID string
	Type       Type
	Period     string
	Status     Status
	// Amount is per share, in the major unit of Currency. A cancellation may carry
	// the amount that was cancelled, or zero.
	Amount   money.Decimal
	Currency money.Currency
	// The dates, YYYY-MM-DD. ExDay is required; the others may be empty. For an
	// estimate they are expected dates.
	ExDay, RecordDay, PayDay, DeclaredDay string
	Source, SourceRef                     string
	// Received is when this system stored the observation.
	Received time.Time
}

// Key of the payout this observes.
func (o Obs) Key() Key { return Key{SecurityID: o.SecurityID, Type: o.Type, Period: o.Period} }

var periodRE = regexp.MustCompile(`^(\d{4})(Q[1-4]|H[12]|-(0[1-9]|1[0-2]))?$`)

func validDay(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil && len(s) == 10
}

// Validate says why an observation cannot be stored, or nil.
func (o Obs) Validate() error {
	at := fmt.Sprintf("distribution of %s %s %s", o.SecurityID, o.Type, o.Period)
	if o.SecurityID == "" {
		return fmt.Errorf("distribution: an observation needs the security (the share class) it is for")
	}
	switch o.Type {
	case Ordinary, Special, ReturnOfCapital, TypeUnknown:
	default:
		return fmt.Errorf("%s: unknown type %q", at, o.Type)
	}
	if !periodRE.MatchString(o.Period) {
		return fmt.Errorf("%s: the period %q is not 2026, 2026H1, 2026Q3 or 2026-11", at, o.Period)
	}
	switch o.Status {
	case Declared, Estimated, Cancelled:
	default:
		return fmt.Errorf("%s: unknown status %q", at, o.Status)
	}
	if !o.Currency.Valid() {
		return fmt.Errorf("%s: the payout currency %q is not a known currency; it is never taken from the listing", at, o.Currency)
	}
	switch {
	case o.Amount.Sign() < 0:
		return fmt.Errorf("%s: negative amount %s", at, o.Amount)
	case o.Amount.Sign() == 0 && o.Status != Cancelled:
		return fmt.Errorf("%s: a %s payout of zero is a cancellation: say cancelled", at, o.Status)
	}
	if !validDay(o.ExDay) {
		return fmt.Errorf("%s: the ex-date %q is not YYYY-MM-DD", at, o.ExDay)
	}
	for name, d := range map[string]string{"record date": o.RecordDay, "pay date": o.PayDay, "declared date": o.DeclaredDay} {
		if d != "" && !validDay(d) {
			return fmt.Errorf("%s: the %s %q is not YYYY-MM-DD", at, name, d)
		}
	}
	if o.RecordDay != "" && o.RecordDay < o.ExDay {
		return fmt.Errorf("%s: record date %s is before the ex-date %s", at, o.RecordDay, o.ExDay)
	}
	if o.PayDay != "" && o.PayDay < o.ExDay {
		return fmt.Errorf("%s: pay date %s is before the ex-date %s", at, o.PayDay, o.ExDay)
	}
	if o.PayDay != "" && o.RecordDay != "" && o.PayDay < o.RecordDay {
		return fmt.Errorf("%s: pay date %s is before the record date %s", at, o.PayDay, o.RecordDay)
	}
	if o.DeclaredDay != "" && o.DeclaredDay > o.ExDay && o.Status == Declared {
		return fmt.Errorf("%s: declared on %s, after the ex-date %s", at, o.DeclaredDay, o.ExDay)
	}
	if strings.TrimSpace(o.Source) == "" {
		return fmt.Errorf("%s has no source", at)
	}
	return nil
}

// ---- periods ----

type periodKind int

const (
	monthly periodKind = iota
	quarterly
	halfYearly
	yearly
)

func (k periodKind) String() string {
	return [...]string{"monthly", "quarterly", "semiannual", "annual"}[k]
}

// parsePeriod returns the year, the 1-based index inside it (month, quarter or
// half; 0 for a year) and the granularity.
func parsePeriod(p string) (year, idx int, kind periodKind, ok bool) {
	m := periodRE.FindStringSubmatch(p)
	if m == nil {
		return 0, 0, 0, false
	}
	fmt.Sscanf(m[1], "%d", &year)
	switch {
	case m[2] == "":
		return year, 0, yearly, true
	case m[2][0] == 'Q':
		fmt.Sscanf(m[2][1:], "%d", &idx)
		return year, idx, quarterly, true
	case m[2][0] == 'H':
		fmt.Sscanf(m[2][1:], "%d", &idx)
		return year, idx, halfYearly, true
	}
	fmt.Sscanf(m[3], "%d", &idx)
	return year, idx, monthly, true
}

func formatPeriod(year, idx int, kind periodKind) string {
	switch kind {
	case yearly:
		return fmt.Sprintf("%04d", year)
	case quarterly:
		return fmt.Sprintf("%04dQ%d", year, idx)
	case halfYearly:
		return fmt.Sprintf("%04dH%d", year, idx)
	}
	return fmt.Sprintf("%04d-%02d", year, idx)
}

// PriorYear is the same period one year earlier.
func PriorYear(p string) string {
	y, i, k, ok := parsePeriod(p)
	if !ok {
		return ""
	}
	return formatPeriod(y-1, i, k)
}

func nextPeriod(p string) string {
	y, i, k, ok := parsePeriod(p)
	if !ok {
		return ""
	}
	per := [...]int{12, 4, 2, 1}[k]
	if k == yearly {
		return formatPeriod(y+1, 0, k)
	}
	if i < per {
		return formatPeriod(y, i+1, k)
	}
	return formatPeriod(y+1, 1, k)
}

func addDays(day string, n int) string {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, n).Format(time.DateOnly)
}
