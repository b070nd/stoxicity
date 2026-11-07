package insider

import (
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/universe"
)

// FigureDays is how many days back the figures look: a quarter, long enough for
// a purchase to be seen, short enough that it is still news.
const FigureDays = 90

// Summary is what the insiders of one company did on the open market in the
// last FigureDays days.
type Summary struct {
	// Buys and Sells are the money moved by purchases and by sales; Buyers and
	// Sellers are the number of different people who made them.
	Buys, Sells       float64
	Buyers, Sellers   int
	LastBuy, LastSell string // day of the newest, YYYY-MM-DD
}

// Summarize groups the open market trades (purchases P and sales S, with a
// price) that were made in the last FigureDays days by company CIK. Awards,
// exercises, tax withholding and gifts are not trades of conviction and are
// left out. A trade without a price cannot be valued and is left out as well.
func Summarize(trades []model.InsiderTrade, now time.Time) map[string]Summary {
	cut := now.AddDate(0, 0, -FigureDays).Format(time.DateOnly)
	out := map[string]Summary{}
	who := map[string]map[string]bool{} // cik + kind -> owners
	for _, t := range trades {
		kind := t.Kind()
		if kind == "" || t.Price <= 0 || t.Shares <= 0 || t.Date < cut || t.CIK == "" {
			continue
		}
		s := out[t.CIK]
		key := t.CIK + "|" + kind
		if who[key] == nil {
			who[key] = map[string]bool{}
		}
		owner := t.OwnerCIK
		if owner == "" {
			owner = t.Owner
		}
		who[key][owner] = true
		switch kind {
		case model.TradeBuy:
			s.Buys += t.Value()
			s.LastBuy = max(s.LastBuy, t.Date)
		case model.TradeSell:
			s.Sells += t.Value()
			s.LastSell = max(s.LastSell, t.Date)
		}
		s.Buyers, s.Sellers = len(who[t.CIK+"|"+model.TradeBuy]), len(who[t.CIK+"|"+model.TradeSell])
		out[t.CIK] = s
	}
	return out
}

// Figures turns the summaries into what a query compares and a list shows, by
// name (buys, sells, buyers, sellers) and then ticker. Only common stocks have
// figures, and a company none of whose insiders traded on the open market has
// none: no figure is not a zero, because the filings are read a few hundred a
// cycle and a company may simply not have been reached yet.
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
	for _, p := range profiles {
		s, ok := sums[p.CIK]
		if !ok || p.CIK == "" || p.Type() != universe.TypeStock {
			continue
		}
		set("buys", p.Ticker, s.Buys)
		set("sells", p.Ticker, s.Sells)
		set("buyers", p.Ticker, float64(s.Buyers))
		set("sellers", p.Ticker, float64(s.Sellers))
	}
	return out
}
