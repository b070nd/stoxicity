package fundamentals

import "github.com/b070nd/stoxicity/app/src/universe"

// Figures turns the summaries into what a query compares and a list shows: by
// name and then ticker. Only common stocks have figures, because the numbers
// belong to the company's shares, not to its warrants, units or preferred
// stock.
//
// Market values are not made here. A share count from the frames cannot be
// attributed to a class, so classcap, issuercap and marketcap come from package
// valuation, which works from the class counts on the cover pages. Where a
// company has several common tickers on the exchanges, the count of shares from
// the frames is not the count of any one ticker's shares, so such a company gets
// no share count.
func Figures(sums map[string]Summary, profiles []universe.Profile) map[string]map[string]float64 {
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
	}
	return out
}
