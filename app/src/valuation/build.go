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
	Splits(ctx context.Context) ([]marketdata.Split, error)
	SplitCoverage(ctx context.Context) (marketdata.SplitCoverage, error)
}

// Set is the valuation of every issuer with a stored count, by SEC number.
type Set struct {
	ByCIK map[string]Value
	// Known is the time the counts and mappings were read as of; zero is now.
	Known time.Time
}

// Build values every issuer. Counts and mappings are read as known at the time
// (zero: now); prices are always the newest stored ones, so a valuation as of a
// past time is not offered: callers pass a past time only to see which counts
// and mappings were known.
func Build(ctx context.Context, src Source, profiles []universe.Profile, known, now time.Time) (Set, error) {
	obs, err := src.ClassObs(ctx)
	if err != nil {
		return Set{}, err
	}
	maps, err := src.ClassMaps(ctx, known)
	if err != nil {
		return Set{}, err
	}
	m, err := LoadMarket(ctx, src)
	if err != nil {
		return Set{}, err
	}
	types := map[string]string{}
	for _, p := range profiles {
		types[marketdata.NormTicker(p.Ticker)] = p.Type()
	}
	byCIK := map[string][]Obs{}
	for _, o := range obs {
		byCIK[o.CIK] = append(byCIK[o.CIK], o)
	}
	set := Set{ByCIK: make(map[string]Value, len(byCIK)), Known: known}
	for cik, os := range byCIK {
		pick, ok := Select(os, known)
		if !ok {
			continue
		}
		mp := map[string]Mapping{}
		for d, mm := range maps[cik] {
			mm.Type = types[marketdata.NormTicker(mm.Ticker)]
			mp[d] = mm
		}
		set.ByCIK[cik] = Compute(pick, mp, m, now)
	}
	return set, nil
}

// LoadMarket reads the prices and splits.
func LoadMarket(ctx context.Context, src Source) (Market, error) {
	latest, err := src.LatestCandles(ctx)
	if err != nil {
		return Market{}, err
	}
	splits, err := src.Splits(ctx)
	if err != nil {
		return Market{}, err
	}
	cov, err := src.SplitCoverage(ctx)
	if err != nil {
		return Market{}, err
	}
	m := Market{Prices: make(map[string]Price, len(latest)), Splits: map[string][]marketdata.Split{}, Coverage: cov}
	for t, cs := range latest {
		if len(cs) == 0 {
			continue
		}
		m.Prices[marketdata.NormTicker(t)] = Price{Ticker: t, Day: cs[0].Day, Close: cs[0].Close, Currency: QuoteCurrency, Raw: cs[0].Raw}
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
