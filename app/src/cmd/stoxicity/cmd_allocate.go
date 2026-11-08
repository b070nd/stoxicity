package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/allocate"
	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/strategy"
)

// cmdAllocate turns strategy proposals into a feasible set of orders under your
// policy limits and a fee schedule you supply. It proposes quantities; it places
// nothing and proposes no sale.
func cmdAllocate(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: plan | example")
	}
	switch args[0] {
	case "plan":
		return allocatePlan(ctx, a, args[1:])
	case "example":
		fmt.Fprint(a.stdout, "security,mic,ticker,sector,price,price_day,fractional,step,min_qty,tradable,facts\nsec_apple,XNAS,AAPL,technology,,,no,,,yes,\n")
		fmt.Fprintln(a.stdout, "# fees.json: "+`{"known": true, "rate": "0.0005", "min": "1.25 EUR", "fixed": "0.00 EUR", "fx_rate": "0.002", "source": "my broker's schedule, 2026-02"}`)
		return nil
	}
	return usageErrorf("unknown subcommand %q (plan, example)", args[0])
}

type feesFile struct {
	Known  bool   `json:"known"`
	Rate   string `json:"rate"`
	Min    string `json:"min"`
	Fixed  string `json:"fixed"`
	FXRate string `json:"fx_rate"`
	Source string `json:"source"`
}

func minorOf(a money.Amount, base money.Currency) (int64, error) {
	if !a.Valid() {
		return 0, nil
	}
	if a.Currency() != base {
		return 0, fmt.Errorf("%s is not in the base currency %s", a, base)
	}
	return a.Minor(), nil
}

func readFees(path string, base money.Currency) (allocate.Fees, error) {
	if path == "" {
		return allocate.Fees{}, nil
	}
	var f feesFile
	if err := readJSONFile(path, &f); err != nil {
		return allocate.Fees{}, err
	}
	out := allocate.Fees{Known: f.Known, Source: f.Source}
	if !f.Known {
		return out, nil
	}
	if strings.TrimSpace(f.Source) == "" {
		return out, errors.New("the fee schedule needs its source: an unsourced schedule is not used")
	}
	var err error
	if f.Rate != "" {
		if out.Rate, err = money.ParseDecimal(f.Rate); err != nil {
			return out, fmt.Errorf("fees rate: %w", err)
		}
	}
	if f.FXRate != "" {
		if out.FXRate, err = money.ParseDecimal(f.FXRate); err != nil {
			return out, fmt.Errorf("fees fx_rate: %w", err)
		}
	}
	for _, x := range []struct {
		s   string
		dst *int64
	}{{f.Min, &out.Min}, {f.Fixed, &out.Fixed}} {
		if x.s == "" {
			continue
		}
		am, err := money.ParseAmount(x.s)
		if err != nil {
			return out, fmt.Errorf("fees: %w", err)
		}
		if *x.dst, err = minorOf(am, base); err != nil {
			return out, err
		}
	}
	return out, nil
}

func parseRates(s string, base money.Currency) (map[money.Currency]money.Decimal, error) {
	out := map[money.Currency]money.Decimal{}
	if s == "" {
		return out, nil
	}
	for _, p := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			return nil, usageErrorf("-rates wants CUR=rate,CUR=rate (base units per one unit of CUR), not %q", p)
		}
		d, err := money.ParseDecimal(v)
		if err != nil || d.Sign() <= 0 {
			return nil, usageErrorf("-rates %s: %q is not a positive rate", k, v)
		}
		out[money.Currency(strings.ToUpper(k))] = d
	}
	_ = base
	return out, nil
}

func yes(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "y", "true", "1":
		return true
	case "no", "n", "false", "0":
		return false
	}
	return def
}

func allocatePlan(ctx context.Context, a *app, args []string) error {
	fs := newFlags("allocate plan")
	candFile := fs.String("candidates", "", "CSV of the securities to consider (allocate example)")
	feeFile := fs.String("fees", "", "JSON fee schedule with its source")
	asOf := fs.String("as-of", "", "the day, YYYY-MM-DD; default today")
	contrib := fs.String("contribution", "", `the regular contribution, e.g. "50.00 EUR", to say how many a purchase takes`)
	rates := fs.String("rates", "", "base units per one unit of a foreign currency, e.g. USD=0.92")
	compare := fs.Bool("compare", false, "plan the fund, stocks and cash options side by side")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *candFile == "" {
		return usageErrorf("usage: allocate plan BOOK -candidates FILE -fees FILE [-as-of DAY] [-contribution A] [-rates USD=0.92] [-compare] [-json]")
	}
	day := a.now().UTC().Format(time.DateOnly)
	if *asOf != "" {
		day = *asOf
	}
	dayT, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return usageErrorf("-as-of must be YYYY-MM-DD")
	}
	known := dayT.Add(24*time.Hour - time.Nanosecond)

	db, err := a.store()
	if err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{})
	if err != nil {
		return err
	}
	if !active {
		return errors.New("no investment policy is active: allocation needs your limits (stoxicity policy example / check / activate)")
	}
	pol := rec.Policy
	base := pol.BaseCurrency
	fees, err := readFees(*feeFile, base)
	if err != nil {
		return err
	}
	rt, err := parseRates(*rates, base)
	if err != nil {
		return err
	}
	var contribution int64
	if *contrib != "" {
		am, err := money.ParseAmount(*contrib)
		if err != nil {
			return usageErrorf("-contribution: %v", err)
		}
		if contribution, err = minorOf(am, base); err != nil {
			return usageErrorf("-contribution: %v", err)
		}
	}
	fracDefault := false
	if v, ok := pol.Account.Fractional.Get(); ok {
		fracDefault = v
	}
	minPos, err := minorOf(pol.Constraints.MinPosition, base)
	if err != nil {
		return fmt.Errorf("policy min_position: %w", err)
	}

	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	l, err := ps.Ledger(ctx, book)
	if err != nil {
		return err
	}
	proj := l.Projection()
	f, err := os.Open(*candFile)
	if err != nil {
		return err
	}
	rows, err := csvtable.Read(f, []string{"security"}, []string{"mic", "ticker", "sector", "price", "price_day", "fractional", "step", "min_qty", "tradable", "facts"})
	f.Close()
	if err != nil {
		return err
	}
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return err
	}
	names := securityNames(reg)
	issuers := map[string]instrument.Issuer{}
	for _, is := range reg.Set().Issuers {
		issuers[is.ID] = is
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	cfg, _, err := loadConfig(ctx, rs)
	if err != nil {
		return err
	}

	type resolved struct {
		sec      instrument.Security
		sector   string
		price    *strategy.Price
		row      map[string]string
		kind     string
		currency money.Currency
	}
	byID := map[string]*resolved{}
	var order []string
	for _, row := range rows {
		sec, ok := names[row["security"]]
		if !ok {
			return fmt.Errorf("line %s: %q is not in the instrument register", row["_line"], row["security"])
		}
		r := &resolved{sec: sec, sector: row["sector"], row: row, kind: "stock"}
		if sec.Kind == instrument.KindETF {
			r.kind = "fund"
		}
		switch {
		case row["price"] != "":
			am, err := money.ParseAmount(row["price"])
			if err != nil {
				return fmt.Errorf("line %s: price: %w", row["_line"], err)
			}
			v, _ := money.ParseDecimal(strings.Fields(row["price"])[0])
			if row["price_day"] == "" {
				return fmt.Errorf("line %s: a supplied price needs price_day", row["_line"])
			}
			r.price = &strategy.Price{Value: v, Currency: am.Currency(), Day: row["price_day"], Source: "supplied"}
		case row["mic"] != "" && row["ticker"] != "":
			pr, _, err := storedPrice(ctx, a, db, row["mic"], row["ticker"], day, known, cfg.MaxPriceAgeDays)
			if err != nil {
				return err
			}
			r.price = pr
		}
		if r.price != nil {
			r.currency = r.price.Currency
		}
		byID[sec.ID] = r
		order = append(order, sec.ID)
	}

	// holdings, valued only where a price exists
	var holdings []allocate.Holding
	heldQty := map[string]bool{}
	for _, pos := range proj.Positions() {
		heldQty[pos.Security] = true
		s := names[pos.Security]
		h := allocate.Holding{Security: pos.Security, Kind: map[bool]string{true: "fund", false: "stock"}[s.Kind == instrument.KindETF], Issuer: s.IssuerID, Country: issuers[s.IssuerID].Country, Currency: base, Sleeve: "core"}
		if r, ok := byID[pos.Security]; ok {
			h.Sector, h.Currency = r.sector, r.currency
			if r.price != nil {
				if v, err := baseNotional(r.price, pos.Qty, base, rt); err == nil {
					h.Value, h.ValueKnown = v, true
				}
			}
		}
		holdings = append(holdings, h)
	}

	// proposals
	type scored struct {
		id    string
		score int
		prop  *strategy.Proposal
	}
	var skipped []allocate.Skip
	var cands []allocate.Candidate
	var picks []scored
	for _, id := range order {
		r := byID[id]
		if r.kind == "fund" {
			if r.row["facts"] == "" {
				skipped = append(skipped, allocate.Skip{Security: id, Reason: "a fund needs its documented facts (the facts column): its checks are not guessed"})
				continue
			}
			var ff strategy.FundFacts
			if err := readJSONFile(r.row["facts"], &ff); err != nil {
				return err
			}
			as := strategy.AssessFund(ff, strategy.DefaultFundConfig(), day)
			if !as.Eligible || !as.Broad {
				skipped = append(skipped, allocate.Skip{Security: id, Reason: "not an eligible broad core fund: " + strings.Join(as.Reasons, "; ")})
				continue
			}
			if r.price == nil {
				skipped = append(skipped, allocate.Skip{Security: id, Reason: "no current price"})
				continue
			}
			cands = append(cands, candidateOf(r.sec, r.row, r.price, "fund", r.sector, issuers, money.Decimal{}, 1, true, false, "", fracDefault))
			continue
		}
		card, err := loadCard(ctx, rs, id, known, 5)
		if err != nil {
			return err
		}
		as, err := loadAssumptions(ctx, rs, id)
		if err != nil {
			return err
		}
		obs, err := db.DistributionObservations(ctx, []string{id}, known)
		if err != nil {
			return err
		}
		in := strategy.Input{Security: id, AsOf: day, Card: card, Payouts: distribution.Resolve(obs, time.Time{}), Assumptions: as, Config: cfg, Held: heldQty[id], Price: r.price}
		p, err := strategy.Evaluate(in)
		if err != nil {
			return err
		}
		if p.Action != strategy.Buy && p.Action != strategy.Add {
			skipped = append(skipped, allocate.Skip{Security: id, Reason: fmt.Sprintf("the strategy says %s: %s", p.Action, firstOr(p.Reasons, "no purchase proposed"))})
			continue
		}
		picks = append(picks, scored{id, p.Score, &p})
	}
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].score != picks[j].score {
			return picks[i].score > picks[j].score
		}
		return picks[i].id < picks[j].id
	})
	for i, pk := range picks {
		r := byID[pk.id]
		var ceiling money.Decimal
		if pk.prop.Valuation != nil {
			ceiling = pk.prop.Valuation.Ceiling
		}
		cands = append(cands, candidateOf(r.sec, r.row, r.price, "stock", r.sector, issuers, ceiling, i+1, false, pk.prop.IncomePreference, pk.prop.NextExDay, fracDefault))
	}

	cons := pol.Constraints
	lim := allocate.Limits{MaxIssuer: cons.MaxIssuer, MaxSector: cons.MaxSector, MaxFund: cons.MaxFund, MaxForeign: cons.MaxCurrency, MaxCountry: cons.MaxCountry,
		CashReserve: pol.Finances.CashReserve, MinPosition: minPos, MaxCostBurden: cons.MaxCostBurden,
		ExcludedSectors: pol.Preferences.ExcludedSectors, ExcludedIssuers: pol.Preferences.ExcludedIssuers}
	req := allocate.Request{Base: base, Cash: proj.Available(base), Holdings: holdings, Candidates: cands, Rates: rt, Limits: lim, Fees: fees, Contribution: contribution}
	if *compare {
		opts := allocate.Compare(req)
		if *asJSON {
			return printJSON(a, map[string]any{"options": opts, "not_considered": skipped})
		}
		t := cliTable{Header: []string{"option", "positions", "spent", "fees", "cash after", "wait", "unknown"}, Right: []bool{false, true, true, true, true}}
		for _, o := range opts {
			w := "no"
			if o.Plan.Wait {
				w = "yes"
			}
			t.Rows = append(t.Rows, []cliCell{styled(o.Name, cliStrong), plain(fmt.Sprint(len(o.Plan.Lines))), plain(minorText(o.Plan.TotalSpend, base)), plain(minorText(o.Plan.TotalFees, base)), plain(minorText(o.Plan.CashAfter, base)), plain(w), plain(fmt.Sprint(len(o.Plan.Unknown)))})
		}
		t.write(a.stdout, useColor(a.stdout))
		for _, o := range opts {
			fmt.Fprintf(a.stdout, "\n%s:\n", o.Name)
			for _, l := range o.Plan.Lines {
				fmt.Fprintf(a.stdout, "  buy %s of %s: %s + %s fees (%s of the order)\n", l.Qty, l.Security, minorText(l.Notional, base), minorText(l.Fee, base), l.Burden)
			}
			for _, e := range append(append([]string{}, o.Plan.Explanation...), o.Plan.Unknown...) {
				fmt.Fprintln(a.stdout, "  - "+e)
			}
		}
		fmt.Fprintln(a.stdout, "\nthe options are not ranked: they differ in cost, concentration and unknowns, and nothing here is a forecast.")
		return nil
	}
	plan := allocate.Allocate(req)
	plan.Skipped = append(skipped, plan.Skipped...)
	if *asJSON {
		return printJSON(a, plan)
	}
	writePlan(a, plan, base)
	return nil
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[len(s)-1]
	}
	return def
}

// baseNotional values qty shares at a price in the base currency.
func baseNotional(p *strategy.Price, qty money.Decimal, base money.Currency, rates map[money.Currency]money.Decimal) (int64, error) {
	native, err := p.Value.Mul(qty, 8, money.HalfEven)
	if err != nil {
		return 0, err
	}
	if p.Currency != base {
		r, ok := rates[p.Currency]
		if !ok {
			return 0, fmt.Errorf("no %s->%s rate", p.Currency, base)
		}
		if native, err = native.Mul(r, 8, money.HalfEven); err != nil {
			return 0, err
		}
	}
	am, err := money.NewAmount(native, base, money.HalfEven)
	if err != nil {
		return 0, err
	}
	return am.Minor(), nil
}

func candidateOf(sec instrument.Security, row map[string]string, price *strategy.Price, kind, sector string, issuers map[string]instrument.Issuer, ceiling money.Decimal, rank int, lookThrough, income bool, nextEx string, fracDefault bool) allocate.Candidate {
	c := allocate.Candidate{Security: sec.ID, Kind: kind, Sleeve: "core", Issuer: sec.IssuerID, Sector: sector, Country: issuers[sec.IssuerID].Country,
		Currency: price.Currency, Price: price.Value, Ceiling: ceiling, Rank: rank, IncomePreference: income, NextExDay: nextEx,
		Fractional: yes(row["fractional"], fracDefault), LookThroughKnown: lookThrough, Tradable: yes(row["tradable"], true)}
	if d, err := money.ParseDecimal(row["step"]); err == nil {
		c.Step = d
	}
	if d, err := money.ParseDecimal(row["min_qty"]); err == nil {
		c.MinQty = d
	}
	return c
}

func writePlan(a *app, p allocate.Plan, base money.Currency) {
	head := "FEASIBLE"
	st := cliGreen
	switch {
	case p.Wait:
		head, st = "WAIT", cliYellow
	case !p.Feasible:
		head, st = "NO FEASIBLE PURCHASE", cliRed
	}
	fmt.Fprintf(a.stdout, "%s   equity %s, spendable after the reserve %s\n\n", styledText(head, st, useColor(a.stdout)), minorText(p.Equity, base), minorText(p.Spendable, base))
	if len(p.Lines) > 0 {
		t := cliTable{Header: []string{"security", "quantity", "notional", "fee", "cost burden", "note"}, Right: []bool{false, true, true, true, true}}
		for _, l := range p.Lines {
			t.Rows = append(t.Rows, []cliCell{styled(l.Security, cliStrong), plain(l.Qty), plain(minorText(l.Notional, base)), plain(minorText(l.Fee, base)), plain(l.Burden), plain(l.Note)})
		}
		t.write(a.stdout, useColor(a.stdout))
		fmt.Fprintf(a.stdout, "\nspent %s, fees %s, cash after %s\n", minorText(p.TotalSpend, base), minorText(p.TotalFees, base), minorText(p.CashAfter, base))
	}
	if len(p.Skipped) > 0 {
		fmt.Fprintln(a.stdout, "\nnot allocated:")
		t := cliTable{Header: []string{"security", "why"}}
		for _, s := range p.Skipped {
			t.Rows = append(t.Rows, []cliCell{plain(s.Security), plain(s.Reason)})
		}
		t.write(a.stdout, useColor(a.stdout))
	}
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(a.stdout, "\n%s:\n", title)
		for _, l := range lines {
			fmt.Fprintln(a.stdout, "  - "+l)
		}
	}
	section("breaches by what you already hold (nothing is sold here)", p.Breaches)
	section("unknown", p.Unknown)
	section("explanation", p.Explanation)
	fmt.Fprintln(a.stdout, "\nthis is a proposal of quantities, not an order: nothing is placed, and a stored plan is not an authorization.")
}

// minorText writes minor units as an amount in the currency.
func minorText(minor int64, cur money.Currency) string {
	a, err := money.FromMinor(minor, cur)
	if err != nil {
		return fmt.Sprint(minor)
	}
	return a.String()
}
