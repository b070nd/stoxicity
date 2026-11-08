package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/paper"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
)

const recPaperConfig = "paper.config"

// cmdPaper runs the local paper engine: it turns finished daily bars into
// reports about orders that were acknowledged in a paper_local book, through
// the same order and ledger contracts as any other fill.
func cmdPaper(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: run | fills | reset | example")
	}
	switch args[0] {
	case "run":
		return paperRun(ctx, a, args[1:])
	case "fills":
		return paperFills(ctx, a, args[1:])
	case "reset":
		return paperReset(ctx, a, args[1:])
	case "example":
		fmt.Fprintln(a.stdout, "# bars.csv: finished days as traded (not adjusted); currency is optional and must match the order")
		fmt.Fprintln(a.stdout, "security,day,open,high,low,close,volume,currency")
		fmt.Fprintln(a.stdout, "sec_apple,2026-11-04,22.10,22.80,21.90,22.50,1200000,USD")
		return nil
	}
	return usageErrorf("unknown subcommand %q (run, fills, reset, example)", args[0])
}

func readBars(path string) ([]paper.Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csvtable.Read(f, []string{"security", "day", "open", "high", "low", "close", "volume"}, []string{"currency"})
	if err != nil {
		return nil, err
	}
	var out []paper.Bar
	for _, r := range rows {
		b := paper.Bar{Security: r["security"], Day: r["day"], Currency: money.Currency(strings.ToUpper(r["currency"]))}
		at := fmt.Sprintf("line %s", r["_line"])
		for name, dst := range map[string]*money.Decimal{"open": &b.Open, "high": &b.High, "low": &b.Low, "close": &b.Close} {
			if *dst, err = money.ParseDecimal(r[name]); err != nil {
				return nil, fmt.Errorf("%s: %s %q: %w", at, name, r[name], err)
			}
		}
		if b.Volume, err = strconv.ParseInt(r["volume"], 10, 64); err != nil {
			return nil, fmt.Errorf("%s: volume %q is not a whole number of shares", at, r["volume"])
		}
		if err := b.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// paperConfig returns the configuration stored with a book, or the one asked
// for when none is stored yet (the second result is then true). A book's configuration never changes: its fills would
// no longer be reproducible.
func paperConfig(ctx context.Context, a *app, bookID string, want paper.Config, given map[string]bool) (paper.Config, bool, error) {
	rs, err := a.records()
	if err != nil {
		return want, false, err
	}
	rec, err := rs.Latest(ctx, recPaperConfig, bookID)
	switch {
	case errors.Is(err, store.ErrNoRecord):
		// nothing is stored until the first run that decides something
		return want, true, want.Validate()
	case err != nil:
		return want, false, err
	}
	var have paper.Config
	if err := json.Unmarshal(rec.Body, &have); err != nil {
		return want, false, fmt.Errorf("the stored paper configuration of %s does not decode: %w", bookID, err)
	}
	var diff []string
	if given["seed"] && have.Seed != want.Seed {
		diff = append(diff, fmt.Sprintf("seed %d is stored, not %d", have.Seed, want.Seed))
	}
	if given["participation"] && have.Participation.Cmp(want.Participation) != 0 {
		diff = append(diff, fmt.Sprintf("participation %s is stored, not %s", have.Participation, want.Participation))
	}
	if given["min-fill"] && have.MinFill.Cmp(want.MinFill) != 0 {
		diff = append(diff, fmt.Sprintf("min-fill %s is stored, not %s", have.MinFill, want.MinFill))
	}
	if given["settle-days"] && have.SettleDays != want.SettleDays {
		diff = append(diff, fmt.Sprintf("settle-days %d is stored, not %d", have.SettleDays, want.SettleDays))
	}
	if len(diff) > 0 {
		return have, false, fmt.Errorf("the paper configuration of this book is fixed so that its fills stay reproducible (%s); reset the book to start with another", strings.Join(diff, "; "))
	}
	return have, false, nil
}

func paperRun(ctx context.Context, a *app, args []string) error {
	fs := newFlags("paper run")
	barsFile := fs.String("bars", "", "CSV of finished daily bars: security, day, open, high, low, close, volume [, currency]")
	feesFile := fs.String("fees", "", "JSON fee schedule in the book's base currency (default: the fees stated on each order)")
	seed := fs.Int64("seed", 1, "seed of the fill draw (fixed for the book on first use)")
	part := fs.String("participation", "0.01", "most of a day's volume one order may take (fixed for the book)")
	minFill := fs.String("min-fill", "0.25", "lowest fraction of that capacity the draw gives (fixed for the book)")
	settle := fs.Int("settle-days", 2, "weekdays after a fill until the cash settles (fixed for the book)")
	through := fs.String("through", "", "last bar day to use (default: the day before today)")
	asJSON := fs.Bool("json", false, "print the outcomes as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *barsFile == "" {
		return usageErrorf("usage: paper run BOOK -bars FILE [-fees FILE] [-seed N] [-through DAY]")
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	pd, err := money.ParseDecimal(*part)
	if err != nil {
		return usageErrorf("-participation: %v", err)
	}
	md, err := money.ParseDecimal(*minFill)
	if err != nil {
		return usageErrorf("-min-fill: %v", err)
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	b, err := ps.FindBook(ctx, book)
	if err != nil {
		return err
	}
	if b.Env != portfolio.PaperLocal {
		return fmt.Errorf("%s/%s is a %s book: the local paper engine only works on paper_local books", b.Account, b.Name, b.Env)
	}
	if b.Status != portfolio.Active {
		return fmt.Errorf("book %s is archived", b.ID)
	}
	yesterday := a.now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	last := *through
	if last == "" {
		last = yesterday
	}
	if _, err := time.Parse(time.DateOnly, last); err != nil {
		return usageErrorf("-through %q is not YYYY-MM-DD", last)
	}
	if last > yesterday {
		return usageErrorf("-through %s is not a finished day: bars up to %s can be used", last, yesterday)
	}
	cfg, fresh, err := paperConfig(ctx, a, b.ID, paper.Config{Seed: *seed, Participation: pd, MinFill: md, SettleDays: *settle}, given)
	if err != nil {
		return err
	}
	bars, err := readBars(*barsFile)
	if err != nil {
		return err
	}
	fees, err := readFees(*feesFile, b.Base)
	if err != nil {
		return err
	}
	fee := func(v order.View, notional int64, qty money.Decimal) int64 {
		if fees.Known {
			return fees.Of(notional, v.Terms.Currency != b.Base)
		}
		return proportionalFee(v, qty)
	}
	if fees.Known {
		views, err := paperViews(ctx, a, b)
		if err != nil {
			return err
		}
		for _, v := range views {
			if v.Terms.Currency != b.Base {
				return fmt.Errorf("the fee schedule is in %s but %s is in %s: leave -fees out to use the fees stated on the order", b.Base, v.ID, v.Terms.Currency)
			}
		}
	}
	views, err := paperViews(ctx, a, b)
	if err != nil {
		return err
	}
	res, err := paper.Simulate(cfg, views, bars, last, fee)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	if fresh && len(res.Outcomes) > 0 {
		rs, err := a.records()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(cfg)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recPaperConfig, Key: b.ID, Body: raw}); err != nil {
			return err
		}
	} else {
		fresh = false
	}
	by := map[string][]order.Reported{}
	var ids []string
	for _, o := range res.Outcomes {
		if _, ok := by[o.Order]; !ok {
			ids = append(ids, o.Order)
		}
		by[o.Order] = append(by[o.Order], o.Report)
	}
	for _, id := range ids {
		if err := svc.Report(ctx, b.ID, id, by[id]...); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	if *asJSON {
		type row struct {
			Order string `json:"order"`
			Day   string `json:"day"`
			Kind  string `json:"kind"`
			Note  string `json:"note"`
			Fill  string `json:"fill,omitempty"`
		}
		out := struct {
			Config  paper.Config `json:"config"`
			Rows    []row        `json:"outcomes"`
			Pending []string     `json:"pending"`
		}{Config: cfg}
		for _, o := range res.Outcomes {
			r := row{Order: o.Order, Day: o.Day, Kind: string(o.Report.Kind), Note: o.Note}
			if f := o.Report.Fill; f != nil {
				r.Fill = fmt.Sprintf("%s at %s", f.Qty, f.Price)
			}
			out.Rows = append(out.Rows, r)
		}
		for _, p := range res.Pending {
			out.Pending = append(out.Pending, p.Order+": "+p.Why)
		}
		return printJSON(a, out)
	}
	if fresh {
		fmt.Fprintf(a.stdout, "paper configuration stored with the book and fixed from now on: seed %d, participation %s, min-fill %s, settle after %d weekdays.\n", cfg.Seed, cfg.Participation, cfg.MinFill, cfg.SettleDays)
	}
	if len(res.Outcomes) == 0 && len(res.Pending) == 0 {
		fmt.Fprintln(a.stdout, "no acknowledged order is waiting for a session.")
		return nil
	}
	t := cliTable{Header: []string{"order", "session", "result", "why"}}
	for _, o := range res.Outcomes {
		t.Rows = append(t.Rows, []cliCell{styled(o.Order, cliStrong), plain(o.Day), plain(paperResult(o.Report)), plain(o.Note)})
	}
	for _, p := range res.Pending {
		t.Rows = append(t.Rows, []cliCell{styled(p.Order, cliStrong), plain("-"), styled("not decided", cliYellow), plain(p.Why)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout, "Daily bars cannot show what happened inside a day: nothing fills in the bar of submission, a touch of the limit is not a fill, the price is the limit and never better, and volume bounds the size. A fill here is a simulation, not evidence of what a market would have given.")
	return nil
}

func paperResult(r order.Reported) string {
	switch r.Kind {
	case order.FillEv:
		return fmt.Sprintf("fill %s at %s", r.Fill.Qty, r.Fill.Price)
	case order.ExpiredEv:
		return "expired"
	case order.CancelledEv:
		return "cancelled"
	}
	return string(r.Kind)
}

// proportionalFee is the order's own stated fee, in proportion to the part
// filled and rounded up.
func proportionalFee(v order.View, qty money.Decimal) int64 {
	if v.Terms.FeeEstimate == 0 {
		return 0
	}
	d, err := money.Int(v.Terms.FeeEstimate).Mul(qty, 0, money.Up)
	if err != nil {
		return v.Terms.FeeEstimate
	}
	q, err := d.Div(v.Terms.Qty, 0, money.Up)
	if err != nil {
		return v.Terms.FeeEstimate
	}
	n, err := strconv.ParseInt(q.String(), 10, 64)
	if err != nil || n > v.Terms.FeeEstimate {
		return v.Terms.FeeEstimate
	}
	return n
}

func paperViews(ctx context.Context, a *app, b portfolio.Book) ([]order.View, error) {
	svc, err := orderService(ctx, a)
	if err != nil {
		return nil, err
	}
	vs, err := svc.Views(ctx, b.Account)
	if err != nil {
		return nil, err
	}
	var out []order.View
	for _, v := range vs {
		if v.Terms.Book == b.ID {
			out = append(out, v)
		}
	}
	return out, nil
}

func paperFills(ctx context.Context, a *app, args []string) error {
	fs := newFlags("paper fills")
	asJSON := fs.Bool("json", false, "print JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: paper fills BOOK")
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	b, err := ps.FindBook(ctx, book)
	if err != nil {
		return err
	}
	ords, err := a.orders()
	if err != nil {
		return err
	}
	logs, err := ords.Logs(ctx, b.Account)
	if err != nil {
		return err
	}
	type fill struct {
		Order, Security, Side, ID, Day, Qty, Price, Fee, InLedger string
	}
	var rows []fill
	var ids []string
	for id := range logs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v, err := order.Fold(logs[id])
		if err != nil || v.Terms.Book != b.ID {
			continue
		}
		for _, e := range logs[id] {
			if e.Kind != order.FillEv || e.Fill == nil {
				continue
			}
			in := "yes"
			if !e.Fill.Posted {
				in = "NO"
			}
			rows = append(rows, fill{id, v.Terms.Security, string(v.Terms.Side), e.Fill.ID, e.Fill.At.UTC().Format(time.DateOnly), e.Fill.Qty.String(), e.Fill.Price.String() + " " + string(v.Terms.Currency), minorText(e.Fill.Fee, v.Terms.Currency), in})
		}
	}
	if *asJSON {
		return printJSON(a, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, "no fills")
		return nil
	}
	t := cliTable{Header: []string{"order", "security", "side", "day", "shares", "price", "fee", "in the ledger", "fill ID"}, Right: []bool{false, false, false, false, true, true, true}}
	for _, r := range rows {
		c := plain(r.InLedger)
		if r.InLedger == "NO" {
			c = styled(r.InLedger, cliRed)
		}
		t.Rows = append(t.Rows, []cliCell{styled(r.Order, cliStrong), plain(r.Security), plain(r.Side), plain(r.Day), plain(r.Qty), plain(r.Price), plain(r.Fee), c, plain(r.ID)})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

// paperReset archives one paper book with its journal and opens an empty one. It
// takes the exact book ID so that nothing else can be meant, and it refuses
// anything that is not a local paper book.
func paperReset(ctx context.Context, a *app, args []string) error {
	fs := newFlags("paper reset")
	name := fs.String("name", "", "name of the new empty book")
	id, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if id == "" || *name == "" || !strings.HasPrefix(id, "bk_") {
		return usageErrorf("usage: paper reset BOOK-ID -name NEW (the exact book ID, bk_..., as portfolio book list shows it)")
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	b, err := ps.FindBook(ctx, id)
	if err != nil {
		return err
	}
	if b.Env != portfolio.PaperLocal {
		return fmt.Errorf("%s is a %s book: paper reset only works on a paper_local book and never touches a real account or a broker's paper account", id, b.Env)
	}
	ords, err := a.orders()
	if err != nil {
		return err
	}
	logs, err := ords.Logs(ctx, b.Account)
	if err != nil {
		return err
	}
	open := 0
	for _, l := range logs {
		if v, err := order.Fold(l); err == nil && v.Terms.Book == b.ID && !v.Terminal() {
			open++
		}
	}
	nb, err := ps.ResetPaper(ctx, id, *name)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "book %s archived with its journal, orders and fills intact; new empty %s book %s/%s opened (id %s). The paper configuration belongs to the old book: the new one starts with its own.\n", id, nb.Env, nb.Account, nb.Name, nb.ID)
	if open > 0 {
		fmt.Fprintf(a.stdout, "%d order(s) of the archived book were still open; they stay in its history and nothing more will happen to them.\n", open)
	}
	return nil
}
