package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/refdata"
)

const defaultQuoteDays = 10

// universeQuotes brings the daily candles of stocks up to date. Without
// tickers it takes the stocks you follow and those in the news first, then the
// rest in ticker order, up to -max; with tickers it takes just those.
func (a *app) universeQuotes(ctx context.Context, args []string) error {
	fs := newFlags("universe quotes")
	maxN := fs.Int("max", defaultQuoteDays, "most market days to ask for (one request each, the whole market in it); 0 means no limit")
	tickers, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if *maxN < 0 {
		return usageErrorf("-max must not be negative")
	}
	if a.cfg.Company.Quotes == "off" {
		return fmt.Errorf("quotes are off: set company.quotes to polygon")
	}
	if a.cfg.Company.PolygonKey == "" {
		return fmt.Errorf("daily candles come from Polygon, which needs a key of your own: set STOXICITY_POLYGON_KEY (in .env.local); a free key allows five requests a minute, enough for the whole market")
	}
	if a.companies().Empty() {
		return fmt.Errorf("no company list yet: run \"stoxicity universe update\"")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	k := a.quoteKeeper(db, client)
	all, err := refdata.QuoteTickers(ctx, db)
	if err != nil {
		return err
	}
	var first []string
	switch {
	case len(tickers) > 0:
		u := a.universe()
		for _, t := range tickers {
			p, ok := u.Get(t)
			if !ok {
				return fmt.Errorf("%s is not in the company list", strings.ToUpper(t))
			}
			first = append(first, p.Ticker)
		}
		if _, byDay := k.Source.(marketdata.PerDay); !byDay { // a source of days brings the whole market: the tickers only choose what is printed
			all = first
			if *maxN == 0 || *maxN < len(first) {
				*maxN = len(first)
			}
		}
	default:
		first = interestingTickers(ctx, db, a.knownTickers(), a.layerPath(), a.cfg.Lens.Threshold, a.now())
	}
	budget := *maxN
	if budget == 0 {
		budget = len(all) + 1000 // no limit; a source of days needs few
	}
	rep, err := k.Refresh(ctx, all, first, budget)
	fmt.Fprintf(a.stdout, "candles (%s)  %s\n", k.Source.Name(), rep)
	stocks, newest, serr := db.CandleStats(ctx)
	if serr == nil && stocks > 0 {
		fmt.Fprintf(a.stdout, "stored        %s stocks with a candle, newest day %s\n", thousandsInt(stocks), newest)
	}
	for _, t := range tickers {
		if p, ok := a.universe().Get(t); ok {
			if cs, _ := db.LatestCandles(ctx); len(cs[p.Ticker]) > 0 {
				c := cs[p.Ticker][0]
				fmt.Fprintf(a.stdout, "%-6s %s  open %.2f  high %.2f  low %.2f  close %.2f  volume %d\n", p.Ticker, c.Day, c.Open, c.High, c.Low, c.Close, c.Volume)
			} else {
				fmt.Fprintf(a.stdout, "%-6s no candle: the source knows nothing of it\n", p.Ticker)
			}
		}
	}
	return err
}
