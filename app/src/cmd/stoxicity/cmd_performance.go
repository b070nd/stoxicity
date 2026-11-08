package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/perf"
)

// readRates reads units of the base currency per unit of a currency, by day.
func readRates(path string) ([]perf.FXRate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csvtable.Read(f, []string{"currency", "day", "rate"}, nil)
	if err != nil {
		return nil, err
	}
	var out []perf.FXRate
	for _, r := range rows {
		v, err := money.ParseDecimal(r["rate"])
		if err != nil {
			return nil, fmt.Errorf("line %s: rate %q: %w", r["_line"], r["rate"], err)
		}
		out = append(out, perf.FXRate{Currency: money.Currency(strings.ToUpper(r["currency"])), Day: r["day"], Value: v})
	}
	return out, nil
}

// benchmarkFile is a comparison, in the file's own words.
type benchmarkFile struct {
	Name          string      `json:"name"`
	Kind          string      `json:"kind"`
	Security      string      `json:"security"`
	Currency      string      `json:"currency"`
	TradeCost     string      `json:"trade_cost"`
	CostSource    string      `json:"cost_source"`
	AnnualRate    string      `json:"annual_rate"`
	RateSource    string      `json:"rate_source"`
	Distributions []perf.Dist `json:"distributions"`
}

func readBenchmark(path string) (perf.Benchmark, error) {
	var f benchmarkFile
	if err := readJSONFile(path, &f); err != nil {
		return perf.Benchmark{}, err
	}
	b := perf.Benchmark{Name: f.Name, Kind: f.Kind, Security: f.Security, Currency: money.Currency(strings.ToUpper(f.Currency)), CostSource: f.CostSource, RateSource: f.RateSource, Distributions: f.Distributions}
	if b.Name == "" {
		b.Name = f.Security
	}
	var err error
	if f.TradeCost != "" {
		if b.TradeCost, err = money.ParseDecimal(f.TradeCost); err != nil {
			return b, fmt.Errorf("%s: trade_cost: %w", path, err)
		}
		b.CostKnown = true
	}
	if f.AnnualRate != "" {
		if b.AnnualRate, err = money.ParseDecimal(f.AnnualRate); err != nil {
			return b, fmt.Errorf("%s: annual_rate: %w", path, err)
		}
	}
	return b, nil
}

func pct(v float64) string { return fmt.Sprintf("%+.4f%%", 100*v) }

func metricCell(m perf.Metric) (cliCell, string) {
	if !m.Available {
		why := m.Reason
		if len(m.Solutions) > 1 {
			var ss []string
			for _, s := range m.Solutions {
				ss = append(ss, pct(s))
			}
			why += ": " + strings.Join(ss, ", ")
		}
		return styled("unavailable", cliYellow), why
	}
	st := cliGreen
	if m.Value < 0 {
		st = cliRed
	}
	note := strings.Join(m.Notes, "; ")
	if m.Annualised != nil {
		note = strings.TrimSpace("annualised " + pct(*m.Annualised) + "; " + note)
	}
	return styled(pct(m.Value), st), strings.TrimSuffix(note, "; ")
}

// cmdPerformance reports what a book earned, apart from the money paid in and out.
func cmdPerformance(ctx context.Context, a *app, args []string) error {
	fs := newFlags("performance")
	marksFile := fs.String("marks", "", "CSV of raw daily closing prices as traded: security, day, open, high, low, close, volume [, currency]")
	ratesFile := fs.String("rates", "", "CSV of exchange rates: currency, day, rate (units of the book's base currency per unit)")
	from := fs.String("from", "", "first day of the period (default: the first transaction)")
	to := fs.String("to", "", "last day of the period (default: today)")
	benchmarks := fs.String("benchmark", "", "comma-separated JSON benchmark files replayed with the same cash flows")
	adjusted := fs.Bool("adjusted", false, "the marks are adjusted closes (refused: they already contain the dividends)")
	maxAge := fs.Int("max-age", perf.DefaultMaxAge, "days after which a mark or rate is reported as stale")
	series := fs.Bool("series", false, "also print the daily value series")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: performance BOOK [-marks FILE] [-rates FILE] [-from DAY] [-to DAY] [-benchmark FILE,...] [-max-age N] [-series] [-json]")
	}
	if *adjusted {
		return usageErrorf("-adjusted: an adjusted-close series already contains the dividends, and the ledger's income would then be counted a second time; use raw closes as traded")
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	l, err := ps.Ledger(ctx, book)
	if err != nil {
		return err
	}
	in := perf.Inputs{Book: l.Book, Txs: l.Txs(), From: *from, To: *to, MaxAge: *maxAge}
	if in.To == "" {
		in.To = a.now().UTC().Format(time.DateOnly)
	}
	if *marksFile != "" {
		bars, err := readBars(*marksFile)
		if err != nil {
			return err
		}
		for _, b := range bars {
			cur := b.Currency
			if cur == "" {
				cur = l.Book.Base
			}
			in.Marks = append(in.Marks, perf.Mark{Security: b.Security, Day: b.Day, Price: b.Close, Currency: cur})
		}
	}
	if *ratesFile != "" {
		if in.Rates, err = readRates(*ratesFile); err != nil {
			return err
		}
	}
	for _, p := range strings.Split(*benchmarks, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		b, err := readBenchmark(p)
		if err != nil {
			return err
		}
		in.Benchmarks = append(in.Benchmarks, b)
	}
	rep, err := perf.Compute(in)
	if err != nil {
		return err
	}
	if !*series {
		rep.Series = nil
	}
	if *asJSON {
		return printJSON(a, rep)
	}
	printPerformance(a, rep)
	return nil
}

func printPerformance(a *app, r perf.Report) {
	color := useColor(a.stdout)
	fmt.Fprintf(a.stdout, "%s  %s to %s  base %s\n", styledText(r.Book, cliStrong, color), r.From, r.To, r.Base)
	t := cliTable{Header: []string{"value", "amount", "note"}}
	row := func(name string, v interface{ String() string }, note string, st cliStyle) {
		t.Rows = append(t.Rows, []cliCell{styled(name, cliStrong), styled(v.String(), st), plain(note)})
	}
	if r.Gain == nil {
		t.Rows = append(t.Rows, []cliCell{styled("value and gain", cliStrong), styled("unavailable", cliYellow), plain(r.GainReason)})
	} else {
		row("start value", r.Start, "market value at the end of the day before the period", cliPlain)
		row("paid in", r.Contributions, "deposits and transfers in: not gain", cliPlain)
		row("taken out", r.Withdrawals, "withdrawals and transfers out: not loss", cliPlain)
		row("end value", r.End, "", cliPlain)
		gs := cliGreen
		if r.Gain.Sign() < 0 {
			gs = cliRed
		}
		row("gain", r.Gain, "end - start - paid in + taken out", gs)
	}
	t.write(a.stdout, color)
	if c := r.Components; c != nil {
		ct := cliTable{Header: []string{"component", "amount", "meaning"}}
		add := func(name string, minor int64, note string) {
			st := cliPlain
			if minor > 0 {
				st = cliGreen
			} else if minor < 0 {
				st = cliRed
			}
			ct.Rows = append(ct.Rows, []cliCell{styled(name, cliStrong), styled(minorText(minor, r.Base), st), plain(note)})
		}
		add("realised gain", c.Realized, "sales against their lots")
		add("unrealised change", c.Unrealized, "market value less cost, now against the start")
		add("income, gross", c.IncomeGross, "dividends and interest booked in the ledger")
		add("withholding", -c.Withholding, "tax taken at source")
		add("trading costs", -c.Costs, "commissions and fees")
		add("transfers at market", -c.Transfers, "value that came with a transfer, not earned here")
		add("FX and translation", c.FX, "the remainder: rates between booking and valuing")
		ct.write(a.stdout, color)
	} else if r.ComponentsReason != "" {
		fmt.Fprintln(a.stdout, "components unavailable: "+r.ComponentsReason)
	}
	rt := cliTable{Header: []string{"measure", "result", "note"}}
	for _, m := range []struct {
		name string
		m    perf.Metric
	}{{"time-weighted return", r.TWR}, {"money-weighted return (IRR)", r.MWR}} {
		c, note := metricCell(m.m)
		rt.Rows = append(rt.Rows, []cliCell{styled(m.name, cliStrong), c, plain(note)})
	}
	if r.Drawdown.Available {
		if r.Drawdown.Trough == "" {
			rt.Rows = append(rt.Rows, []cliCell{styled("max drawdown", cliStrong), styled("none", cliGreen), plain("the index never fell below an earlier high in the period")})
		} else {
			rt.Rows = append(rt.Rows, []cliCell{styled("max drawdown", cliStrong), styled(pct(r.Drawdown.Max), cliRed), plain(fmt.Sprintf("peak %s, trough %s, recovered %s; now %s", r.Drawdown.Peak, r.Drawdown.Trough, orNot(r.Drawdown.Recovered, "not yet"), pct(r.Drawdown.Current)))})
		}
	} else {
		rt.Rows = append(rt.Rows, []cliCell{styled("max drawdown", cliStrong), styled("unavailable", cliYellow), plain(r.Drawdown.Reason)})
	}
	rt.write(a.stdout, color)
	for _, s := range r.StaleMarks {
		fmt.Fprintln(a.stdout, styledText("stale: ", cliYellow, color)+s)
	}
	if len(r.Baselines) > 0 {
		bt := cliTable{Header: []string{"baseline", "end value", "gain", "book less baseline", "TWR", "IRR", "note"}}
		for _, b := range r.Baselines {
			if !b.Available {
				bt.Rows = append(bt.Rows, []cliCell{styled(b.Name, cliStrong), styled("unavailable", cliYellow), plain(""), plain(""), plain(""), plain(""), plain(b.Reason)})
				continue
			}
			tc, _ := metricCell(b.TWR)
			mc, mnote := metricCell(b.MWR)
			if !b.MWR.Available {
				b.Notes = append(b.Notes, "IRR "+mnote)
			}
			ex := cliGreen
			if b.Excess.Sign() < 0 {
				ex = cliRed
			}
			bt.Rows = append(bt.Rows, []cliCell{styled(b.Name, cliStrong), plain(b.End.String()), plain(b.Gain.String()), styled(b.Excess.String(), ex), tc, mc, plain(strings.Join(b.Notes, "; "))})
		}
		bt.write(a.stdout, color)
		fmt.Fprintln(a.stdout, "A baseline gets the same deposits and withdrawals on the same days. A positive difference is not skill: it is one period of one comparison.")
	}
	if len(r.Series) > 0 {
		st := cliTable{Header: []string{"day", "value", "flow", "note"}}
		for _, p := range r.Series {
			v, note := "unavailable", p.Unavailable
			if p.Value != nil {
				v = minorText(*p.Value, r.Base)
			}
			if p.Stale {
				note = strings.TrimSpace(note + " stale mark")
			}
			st.Rows = append(st.Rows, []cliCell{plain(p.Day), plain(v), plain(minorText(p.Flow, r.Base)), plain(note)})
		}
		st.write(a.stdout, color)
	}
	fmt.Fprintln(a.stdout, "Conventions: raw marks, income only from the ledger, flows at the end of the day, nothing filled in for a missing price or rate (docs/performance.md).")
}

func orNot(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
