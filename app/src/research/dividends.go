package research

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/store"
)

// distributionStore is what a study that counts dividends needs of the database on top of Store;
// *store.DB satisfies it. It is asked of the store only when the study asks for dividends.
type distributionStore interface {
	DistributionObservations(ctx context.Context, securityIDs []string, knownAt time.Time) ([]distribution.Obs, error)
	InstrumentsAsOf(ctx context.Context, t time.Time) (instrument.Set, error)
}

var _ distributionStore = (*store.DB)(nil)

// withDividends adds to the prices the cash distributions of the tickers (the securities of the
// events and the benchmarks, which a study with total_return counts like the stocks), read as the
// database held them at asOf (zero: as they stand). It is called only for a study with total_return.
//
// A distribution belongs to a share class, a study's series to a ticker, and the instrument
// registry is what ties them: a ticker is known when a listing carries it, and a distribution
// counts for it when a listing of the ticker, of the distribution's share class, was active on its
// ex-day (a reused ticker does not collect another company's dividends). A ticker no listing
// carries is left unknown and its events end as no_dividends; a known ticker with no distribution
// is taken to pay none, which the stored data cannot tell from a missing record.
//
// Only a declared payout is counted. A cancelled one is dropped. An estimated or conflicting one,
// one in another currency than the listing or quoted in a subunit of it, and one whose listings
// disagree are passed on as unsettled, so that a holding over their ex-day is left out. The amount,
// as the issuer stated it per share on the ex-day, is divided by the ratios of the splits after the
// ex-day (distribution.AdjustForSplits states the rule) with the splits the prices were put on one
// basis with, so it is on the basis of the bars.
func withDividends(ctx context.Context, s Store, prices eventstudy.Prices, tickers []string, asOf time.Time) (eventstudy.Prices, error) {
	ds, ok := s.(distributionStore)
	if !ok {
		return nil, fmt.Errorf("research: the store does not hold distributions")
	}
	set, err := ds.InstrumentsAsOf(ctx, asOf)
	if err != nil {
		return nil, fmt.Errorf("research: instruments: %w", err)
	}
	var splits []marketdata.Split
	if asOf.IsZero() {
		splits, err = s.Splits(ctx)
	} else {
		splits, err = s.SplitsAsOf(ctx, asOf)
	}
	if err != nil {
		return nil, fmt.Errorf("research: splits: %w", err)
	}

	listings := map[string][]instrument.Listing{}
	for _, l := range set.Listings {
		t := marketdata.NormTicker(l.Ticker)
		listings[t] = append(listings[t], l)
	}
	out := eventstudy.MapDividends{}
	var ids []string
	for _, t := range uniqueSorted(tickers) {
		ls := listings[marketdata.NormTicker(t)]
		if len(ls) == 0 {
			continue
		}
		out[t] = nil
		for _, l := range ls {
			ids = append(ids, l.SecurityID)
		}
	}
	ids = uniqueSorted(ids)
	if len(ids) == 0 {
		return eventstudy.PricesWithDividends{Prices: prices, DividendSource: out}, nil
	}
	obs, err := ds.DistributionObservations(ctx, ids, asOf)
	if err != nil {
		return nil, fmt.Errorf("research: distributions: %w", err)
	}
	payouts := distribution.Resolve(obs, asOf)
	for _, t := range uniqueSorted(tickers) {
		if _, known := out[t]; !known {
			continue
		}
		ls := listings[marketdata.NormTicker(t)]
		for _, p := range payouts {
			if p.State == distribution.StateCancelled {
				continue
			}
			d, mine, err := dividendOf(p, t, ls, splits)
			if err != nil {
				return nil, err
			}
			if mine {
				out[t] = append(out[t], d)
			}
		}
		sort.SliceStable(out[t], func(i, j int) bool {
			if out[t][i].ExDay != out[t][j].ExDay {
				return out[t][i].ExDay < out[t][j].ExDay
			}
			return out[t][i].Amount < out[t][j].Amount
		})
	}
	return eventstudy.PricesWithDividends{Prices: prices, DividendSource: out}, nil
}

// dividendOf turns one payout into the distribution of the ticker whose listings are given; false
// when no listing of the ticker carried the payout's share class on its ex-day.
func dividendOf(p distribution.Payout, ticker string, listings []instrument.Listing, splits []marketdata.Split) (eventstudy.Dividend, bool, error) {
	var on []instrument.Listing
	for _, l := range listings {
		if l.SecurityID == p.Key.SecurityID && l.Active(p.ExDay) {
			on = append(on, l)
		}
	}
	if len(on) == 0 {
		return eventstudy.Dividend{}, false, nil
	}
	amount, err := strconv.ParseFloat(p.Amount.String(), 64)
	if err != nil {
		return eventstudy.Dividend{}, false, fmt.Errorf("research: distribution %s amount %q: %w", p.Key, p.Amount, err)
	}
	for _, sp := range splits {
		if marketdata.SameTicker(sp.Ticker, ticker) && sp.Day > p.ExDay {
			amount /= sp.Ratio()
		}
	}
	d := eventstudy.Dividend{ExDay: p.ExDay, Amount: amount, Currency: string(p.Currency)}
	switch {
	case p.State == distribution.StateEstimated:
		d.Unsettled = "only estimated, not declared"
	case p.State == distribution.StateConflicting:
		d.Unsettled = "sources disagree: " + strings.Join(p.Reasons, "; ")
	default:
		for _, l := range on[1:] {
			if l.Currency != on[0].Currency || l.QuoteUnit != on[0].QuoteUnit {
				d.Unsettled = "listings of the ticker differ in currency or quote unit"
			}
		}
		switch {
		case d.Unsettled != "":
		case p.Currency != "" && string(p.Currency) != on[0].Currency:
			d.Unsettled = fmt.Sprintf("paid in %s, the listing trades in %s", p.Currency, on[0].Currency)
		case on[0].QuoteUnit != "" && on[0].QuoteUnit != on[0].Currency:
			d.Unsettled = fmt.Sprintf("the listing is quoted in %s, a subunit of %s", on[0].QuoteUnit, on[0].Currency)
		}
	}
	return d, true, nil
}
