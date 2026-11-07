package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/model"
)

// universeInsiders reads the Form 4 filings the feed found and keeps their
// trades, then prints what the insiders purchased and sold. With a ticker
// it lists that company's trades instead (no filing is read).
func (a *app) universeInsiders(ctx context.Context, args []string) error {
	fs := newFlags("universe insiders")
	limit := fs.Int("max", a.cfg.Company.InsidersPerCycle, "read at most N unread Form 4 filings")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return usageErrorf("insiders takes at most one ticker")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		if a.cfg.Company.Insiders == "off" {
			return fmt.Errorf("insiders are off: set company.insiders to sec")
		}
		client, err := a.httpClient()
		if err != nil {
			return err
		}
		rep, err := a.insiderKeeper(db, client).Refresh(ctx, *limit)
		fmt.Fprintf(a.stdout, "insiders: read %d filings: %d trades, %d without a trade in ordinary shares, %d unreadable, %d left\n",
			rep.Asked, rep.Trades, rep.Empty, rep.Bad, rep.Left)
		if err != nil {
			return err
		}
	}

	since := a.now().AddDate(0, 0, -insider.FigureDays).Format(time.DateOnly)
	cik := ""
	if len(rest) == 1 {
		p, ok := a.universe().Get(rest[0])
		if !ok || p.CIK == "" {
			return fmt.Errorf("%s has no SEC number in the company list", strings.ToUpper(rest[0]))
		}
		cik, since = p.CIK, a.now().AddDate(-1, 0, 0).Format(time.DateOnly)
	}
	trades, err := db.InsiderTrades(ctx, since, cik, 0)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	if cik != "" {
		fmt.Fprintln(tw, "DATE\tOWNER\tROLE\tWHAT\tSHARES\tPRICE\tVALUE\tHELD AFTER\tNOTE")
		for _, t := range trades {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.Date, t.Owner, t.Role, model.InsiderCodeLabel(t.Code),
				countText(t.Shares), moneyOrBlank(t.Price), moneyOrBlank(t.Value()), countText(t.After), tradeNote(t))
		}
		if len(trades) == 0 {
			fmt.Fprintln(a.stdout, "no insider trade stored for this company in the last year")
		}
		return nil
	}
	sums := insider.Summarize(trades, a.now())
	byCIK := map[string]string{}
	for _, p := range a.universe().All() {
		if p.CIK != "" && (byCIK[p.CIK] == "" || p.Ticker < byCIK[p.CIK]) {
			byCIK[p.CIK] = p.Ticker
		}
	}
	type row struct {
		ticker string
		insider.Summary
	}
	var rows []row
	for c, s := range sums {
		if s.Buys > 0 {
			label := byCIK[c]
			if label == "" {
				label = "CIK " + c // not listed on an exchange we know
			}
			rows = append(rows, row{label, s})
		}
	}
	slices.SortFunc(rows, func(x, y row) int { return cmp.Compare(y.Buys, x.Buys) })
	fmt.Fprintf(a.stdout, "purchases of common stock in the last %d days: %d companies\n", insider.FigureDays, len(rows))
	fmt.Fprintln(tw, "TICKER\tBUYS\tBUYERS\tLAST BUY\tSELLS\tSELLERS")
	for i, r := range rows {
		if i == 20 {
			break
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%d\n", r.ticker, moneyOrBlank(r.Buys), r.Buyers, r.LastBuy, moneyOrBlank(r.Sells), r.Sellers)
	}
	return nil
}

func tradeNote(t model.InsiderTrade) string {
	var n []string
	if t.Plan {
		n = append(n, "10b5-1 plan")
	}
	if !t.Direct {
		n = append(n, "held indirectly")
	}
	return strings.Join(n, ", ")
}

func countText(v float64) string {
	if v == 0 {
		return ""
	}
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.2f", v)
}

func moneyOrBlank(v float64) string {
	if v <= 0 {
		return ""
	}
	return fmt.Sprintf("%.2f", v)
}
