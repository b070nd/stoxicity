package valuation

import (
	"context"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// Source is where the inputs are kept (the store).
type Source interface {
	// ClassObs returns every stored share-count observation.
	ClassObs(ctx context.Context) ([]Obs, error)
	// ClassMaps returns the mappings in force at the time (zero: now), by issuer
	// and then by dimensions: the latest mapping of each class made by then.
	ClassMaps(ctx context.Context, at time.Time) (map[string]map[string]Mapping, error)
	// LatestCandles returns the newest candles of each stock, newest first.
	LatestCandles(ctx context.Context) (map[string][]marketdata.Candle, error)
	// CandlesAsOf returns the newest candles of each stock that were public by at
	// and, when known is not zero, held by this system by then (see the store).
	CandlesAsOf(ctx context.Context, at, known time.Time) (map[string][]marketdata.Candle, error)
	Splits(ctx context.Context) ([]marketdata.Split, error)
	SplitCoverage(ctx context.Context) (marketdata.SplitCoverage, error)
	// SplitsAsOf and SplitCoverageAsOf are the split list and what it covered as
	// held by this system by the time (zero: now).
	SplitsAsOf(ctx context.Context, known time.Time) ([]marketdata.Split, error)
	SplitCoverageAsOf(ctx context.Context, known time.Time) (marketdata.SplitCoverage, error)
}

// Set is the valuation of every issuer with a stored count, by SEC number.
type Set struct {
	ByCIK map[string]Value
	// View is what the set is as of.
	View View
}

// Annotate fills in, from the universe, what a mapping needs and does not store:
// the kind of security and the exchange it is listed on.
func Annotate(maps map[string]Mapping, profiles []universe.Profile) map[string]Mapping {
	type info struct{ typ, venue string }
	by := map[string]info{}
	for _, p := range profiles {
		by[marketdata.NormTicker(p.Ticker)] = info{p.Type(), p.Exchange}
	}
	out := make(map[string]Mapping, len(maps))
	for d, m := range maps {
		i := by[marketdata.NormTicker(m.Ticker)]
		m.Type, m.Venue = i.typ, i.venue
		out[d] = m
	}
	return out
}

// Build values every issuer as the view has it. For a known view the counts,
// mappings, prices and splits are those this installation had stored by the
// time; for a reconstructed view, those the record supports as of the time (see
// Mode). The ages of the inputs are measured against the view's time, not the
// wall clock.
func Build(ctx context.Context, src Source, profiles []universe.Profile, view View, now time.Time) (Set, error) {
	obs, err := src.ClassObs(ctx)
	if err != nil {
		return Set{}, err
	}
	var mapAt time.Time
	if view.Mode == ModeKnown {
		mapAt = view.At
	}
	maps, err := src.ClassMaps(ctx, mapAt)
	if err != nil {
		return Set{}, err
	}
	m, err := LoadMarketView(ctx, src, view)
	if err != nil {
		return Set{}, err
	}
	byCIK := map[string][]Obs{}
	for _, o := range obs {
		byCIK[o.CIK] = append(byCIK[o.CIK], o)
	}
	set := Set{ByCIK: make(map[string]Value, len(byCIK)), View: view}
	for cik, os := range byCIK {
		pick, ok := SelectView(os, view)
		if !ok {
			continue
		}
		set.ByCIK[cik] = Compute(pick, Annotate(maps[cik], profiles), m, view.Ref(now))
	}
	return set, nil
}

// LoadMarket reads the newest prices and all the splits.
func LoadMarket(ctx context.Context, src Source) (Market, error) {
	return LoadMarketView(ctx, src, View{})
}

// LoadMarketView reads the prices and splits as the view has them.
func LoadMarketView(ctx context.Context, src Source, view View) (Market, error) {
	var latest map[string][]marketdata.Candle
	var splits []marketdata.Split
	var cov marketdata.SplitCoverage
	var err error
	if view.Historical() {
		var heldBy time.Time
		if view.Mode == ModeKnown {
			heldBy = view.At
		}
		if latest, err = src.CandlesAsOf(ctx, view.At, heldBy); err != nil {
			return Market{}, err
		}
		if splits, err = src.SplitsAsOf(ctx, heldBy); err != nil {
			return Market{}, err
		}
		if cov, err = src.SplitCoverageAsOf(ctx, heldBy); err != nil {
			return Market{}, err
		}
	} else {
		if latest, err = src.LatestCandles(ctx); err != nil {
			return Market{}, err
		}
		if splits, err = src.Splits(ctx); err != nil {
			return Market{}, err
		}
		if cov, err = src.SplitCoverage(ctx); err != nil {
			return Market{}, err
		}
	}
	m := Market{Prices: make(map[string]Price, len(latest)), Splits: map[string][]marketdata.Split{}, Coverage: cov, View: view}
	for t, cs := range latest {
		if len(cs) == 0 {
			continue
		}
		m.Prices[marketdata.NormTicker(t)] = Price{Ticker: t, Day: cs[0].Day, Close: cs[0].Close, Currency: cs[0].Currency, Raw: cs[0].Raw, Fetched: cs[0].FetchedAt}
	}
	for _, s := range splits {
		k := marketdata.NormTicker(s.Ticker)
		m.Splits[k] = append(m.Splits[k], s)
	}
	return m, nil
}

// The names of the figures.
const (
	ClassCap  = "classcap"
	IssuerCap = "issuercap"
	// MarketCap is the issuer's capitalisation: the value of every class of the
	// issuer together, the same for each of its listed classes.
	MarketCap = "marketcap"
)

// Figures gives, by figure name and then ticker, the values that exist, and in
// why, by figure name and ticker, the reason each missing value is missing. Only
// common stock and depositary receipts of issuers with a SEC number are covered.
//
// classcap is the value of the class a ticker is; issuercap and marketcap (the
// same number) the value of the whole issuer, given to each ticker that is one
// of its classes.
func (s Set) Figures(profiles []universe.Profile) (nums map[string]map[string]float64, why map[string]map[string]string) {
	nums, why = map[string]map[string]float64{}, map[string]map[string]string{}
	set := func(name, ticker string, v float64) {
		if nums[name] == nil {
			nums[name] = map[string]float64{}
		}
		nums[name][ticker] = v
	}
	no := func(name, ticker, reason string) {
		if why[name] == nil {
			why[name] = map[string]string{}
		}
		why[name][ticker] = reason
	}
	for _, p := range profiles {
		if p.CIK == "" || (p.Type() != universe.TypeStock && p.Type() != universe.TypeADR) {
			continue
		}
		v, ok := s.ByCIK[p.CIK]
		if !ok {
			r := "the cover page of the company's latest report has not been read"
			for _, n := range []string{ClassCap, IssuerCap, MarketCap} {
				no(n, p.Ticker, r)
			}
			continue
		}
		var cv *ClassValue
		for i := range v.Classes {
			if v.Classes[i].Mapping.Status == Mapped && marketdata.SameTicker(v.Classes[i].Mapping.Ticker, p.Ticker) {
				cv = &v.Classes[i]
				break
			}
		}
		if cv == nil {
			r := fmt.Sprintf("%s is not mapped to a class on the cover page of %s", p.Ticker, v.Accession)
			for _, n := range []string{ClassCap, IssuerCap, MarketCap} {
				no(n, p.Ticker, r)
			}
			continue
		}
		if cv.OK {
			set(ClassCap, p.Ticker, cv.Cap)
		} else {
			no(ClassCap, p.Ticker, cv.Reason)
		}
		if v.IssuerOK {
			set(IssuerCap, p.Ticker, v.Issuer)
			set(MarketCap, p.Ticker, v.Issuer)
		} else {
			no(IssuerCap, p.Ticker, v.IssuerReason)
			no(MarketCap, p.Ticker, v.IssuerReason)
		}
	}
	return nums, why
}
