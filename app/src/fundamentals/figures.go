package fundamentals

import "github.com/b070nd/stoxicity/app/src/universe"

// Figures turns the summaries into what a query compares and a list shows: by
// name and then ticker. Only common stocks have figures, because the numbers
// belong to the company's shares, not to its warrants, units or preferred
// stock. closes are the last closes by ticker; with one, the stock also has a
// market value (shares outstanding times close). Where a company has several
// classes of shares on the exchanges the cover page may give the shares of one
// class, so such a company, with more than one common ticker on the exchanges,
// gets no share count and no market value.
func Figures(sums map[string]Summary, profiles []universe.Profile, closes map[string]float64) map[string]map[string]float64 {
	out := map[string]map[string]float64{}
	if len(sums) == 0 {
		return out
	}
	set := func(name, ticker string, v float64) {
		if out[name] == nil {
			out[name] = map[string]float64{}
		}
		out[name][ticker] = v
	}
	// A company with shares of more than one class listed has one share count on
	// its cover page for whichever class the filer chose to give, or the sum of
	// the classes. Either way it is not the number of this ticker's shares, so
	// the share count and market value of such a company are not shown at all.
	classes := map[string]int{}
	for _, p := range profiles {
		if p.CIK != "" && p.Type() == universe.TypeStock {
			classes[p.CIK]++
		}
	}
	for _, p := range profiles {
		if p.CIK == "" || p.Type() != universe.TypeStock {
			continue
		}
		s, ok := sums[p.CIK]
		if !ok {
			continue
		}
		multi := classes[p.CIK] > 1
		for name, v := range s.Num {
			if multi && (name == Shares || name == AvgShares) {
				continue
			}
			set(name, p.Ticker, v)
		}
		if multi {
			continue
		}
		// A foreign issuer that files under IFRS has shares on its cover page but
		// no US GAAP figures; its shares are ordinary shares and the price is
		// that of a receipt for several of them, so no market value is made.
		_, usGAAP := s.Num[Revenue]
		_, hasIncome := s.Num[NetIncome]
		_, hasAssets := s.Num[Assets]
		if mc, ok := s.MarketCap(closes[p.Ticker]); ok && (usGAAP || hasIncome || hasAssets) {
			set("marketcap", p.Ticker, mc)
		}
	}
	return out
}
