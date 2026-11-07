package fundamentals

import "github.com/b070nd/stoxicity/app/src/universe"

// Figures turns the summaries into what a query compares and a list shows: by
// name and then ticker. Only common stocks have figures, because the numbers
// belong to the company's shares, not to its warrants, units or preferred
// stock. closes are the last closes by ticker; with one, the stock also has a
// market value (shares outstanding times close). Where a company has several
// classes of shares on the exchanges the cover page may give the shares of one
// class, so the market value of such a company is only as good as that count.
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
	for _, p := range profiles {
		if p.CIK == "" || p.Type() != universe.TypeStock {
			continue
		}
		s, ok := sums[p.CIK]
		if !ok {
			continue
		}
		for name, v := range s.Num {
			set(name, p.Ticker, v)
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
