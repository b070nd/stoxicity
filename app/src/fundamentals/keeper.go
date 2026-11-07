package fundamentals

import (
	"context"
	"time"
)

// DefaultMaxAge is how old the stored facts may get: filings arrive daily but
// a company's year changes four times a year at most, so a week is plenty.
const DefaultMaxAge = 7 * 24 * time.Hour

// Store keeps the facts.
type Store interface {
	// PutFundamentals stores the facts (replacing those of the same company,
	// metric and period) and notes that the market was read at the time.
	PutFundamentals(ctx context.Context, facts []Fact, at time.Time) error
	// FundamentalsRead is when the market was last read; zero if never.
	FundamentalsRead(ctx context.Context) (time.Time, error)
}

// Keeper keeps the stored facts up to date.
type Keeper struct {
	Store   Store
	Fetcher Fetcher
	// MaxAge is how old the last reading may be before another is made; zero
	// means DefaultMaxAge.
	MaxAge time.Duration
	Now    func() time.Time
}

func (k *Keeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// Refresh reads the market if the last reading is older than MaxAge (or force).
// ran says whether it did.
func (k *Keeper) Refresh(ctx context.Context, force bool) (rep Report, ran bool, err error) {
	age := k.MaxAge
	if age <= 0 {
		age = DefaultMaxAge
	}
	if !force {
		last, err := k.Store.FundamentalsRead(ctx)
		if err != nil {
			return rep, false, err
		}
		if !last.IsZero() && k.now().Sub(last) < age {
			return rep, false, nil
		}
	}
	if k.Fetcher.Now == nil {
		k.Fetcher.Now = k.Now
	}
	facts, rep, err := k.Fetcher.Fetch(ctx)
	if err != nil {
		return rep, true, err
	}
	return rep, true, k.Store.PutFundamentals(ctx, facts, k.now())
}
