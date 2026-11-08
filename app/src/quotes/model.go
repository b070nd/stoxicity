// Package quotes holds exchange-rate and price observations: exact values with
// the day they are for, who gave them, what they are (a reference rate or a fill,
// a raw or adjusted price, the unit it is quoted in) and when this system
// received them. It decides whether a value may be used for an action: a stale,
// conflicting or unit-ambiguous one is refused with the reason, never repaired.
package quotes

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// FXObs is one exchange-rate observation: 1 Base = Value Quote, for Day.
type FXObs struct {
	Seq   int64
	Base  money.Currency
	Quote money.Currency
	Value money.Decimal
	Basis money.Basis
	// Day is the day the rate is for (the ECB reference date), YYYY-MM-DD.
	Day string
	// Source is who gave it ("ecb", "user:file-name"); SourceRef points at the
	// document: a URL, a file hash, a broker statement line.
	Source    string
	SourceRef string
	// Received is when this system stored it. Not when the source published it.
	Received time.Time
}

// Validate says why an observation cannot be stored, or nil.
func (o FXObs) Validate() error {
	if _, err := money.NewRate(o.Base, o.Quote, o.Value, o.Basis); err != nil {
		return err
	}
	if !validDay(o.Day) {
		return fmt.Errorf("quotes: rate %s/%s: the day %q is not YYYY-MM-DD", o.Base, o.Quote, o.Day)
	}
	if strings.TrimSpace(o.Source) == "" {
		return fmt.Errorf("quotes: rate %s/%s on %s has no source", o.Base, o.Quote, o.Day)
	}
	if o.Basis == money.BasisFill && strings.TrimSpace(o.SourceRef) == "" {
		return fmt.Errorf("quotes: a fill rate %s/%s on %s must point at the statement or confirmation it came from", o.Base, o.Quote, o.Day)
	}
	return nil
}

// Rate is the observation as a money.Rate.
func (o FXObs) Rate() (money.Rate, error) { return money.NewRate(o.Base, o.Quote, o.Value, o.Basis) }

// Kind of price.
type Kind string

const (
	Close Kind = "close"
	Last  Kind = "last"
)

// Adjustment says what corporate actions the price has been adjusted for.
type Adjustment string

const (
	Raw           Adjustment = "raw"            // as traded on the day
	SplitAdjusted Adjustment = "split_adjusted" // restated for splits
	TotalReturn   Adjustment = "total_return"   // restated for splits and distributions
	AdjUnknown    Adjustment = "unknown"        // the source does not say
)

// Delay says how late the price was when it was taken.
type Delay string

const (
	EndOfDay Delay = "eod"
	RealTime Delay = "realtime"
	Delayed  Delay = "delayed"
	DelayUnk Delay = "unknown"
)

// UnitBasis says where the unit of a price came from.
type UnitBasis string

const (
	// UnitStated: the source or the import file states the unit.
	UnitStated UnitBasis = "stated"
	// UnitFromListing: the source is silent and the unit was taken from the
	// instrument record. Good enough to look at, not to size an order.
	UnitFromListing UnitBasis = "listing"
)

// PriceObs is one price of one listing on one day.
type PriceObs struct {
	Seq        int64
	ListingID  string
	Day        string
	Kind       Kind
	Price      money.Price
	UnitBasis  UnitBasis
	Adjustment Adjustment
	Delay      Delay
	// QuoteTime is when the price was struck, when the source says; zero when not.
	QuoteTime time.Time
	Source    string
	SourceRef string
	Received  time.Time
}

// Validate says why an observation cannot be stored, or nil.
func (o PriceObs) Validate() error {
	if o.ListingID == "" {
		return errors.New("quotes: a price needs the listing it is for")
	}
	if !validDay(o.Day) {
		return fmt.Errorf("quotes: price of %s: the day %q is not YYYY-MM-DD", o.ListingID, o.Day)
	}
	switch o.Kind {
	case Close, Last:
	default:
		return fmt.Errorf("quotes: price of %s on %s: unknown kind %q", o.ListingID, o.Day, o.Kind)
	}
	if _, err := money.NewPrice(o.Price.Value, o.Price.Unit); err != nil {
		return fmt.Errorf("quotes: price of %s on %s: %w", o.ListingID, o.Day, err)
	}
	if o.Price.Value.Sign() == 0 {
		return fmt.Errorf("quotes: price of %s on %s is zero", o.ListingID, o.Day)
	}
	switch o.UnitBasis {
	case UnitStated, UnitFromListing:
	default:
		return fmt.Errorf("quotes: price of %s on %s: the unit basis %q is not stated or listing", o.ListingID, o.Day, o.UnitBasis)
	}
	switch o.Adjustment {
	case Raw, SplitAdjusted, TotalReturn, AdjUnknown:
	default:
		return fmt.Errorf("quotes: price of %s on %s: unknown adjustment %q", o.ListingID, o.Day, o.Adjustment)
	}
	switch o.Delay {
	case EndOfDay, RealTime, Delayed, DelayUnk:
	default:
		return fmt.Errorf("quotes: price of %s on %s: unknown delay %q", o.ListingID, o.Day, o.Delay)
	}
	if strings.TrimSpace(o.Source) == "" {
		return fmt.Errorf("quotes: price of %s on %s has no source", o.ListingID, o.Day)
	}
	return nil
}

func validDay(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil && len(s) == 10
}

// DaysBetween is the number of calendar days from a to b (negative if b is
// earlier). False when either is not a day.
func DaysBetween(a, b string) (int, bool) {
	ta, err1 := time.Parse(time.DateOnly, a)
	tb, err2 := time.Parse(time.DateOnly, b)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return int(tb.Sub(ta).Hours() / 24), true
}
