package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/income"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cmdIncome answers what a book is owed by the declared payouts and books it:
// entitlement from the fills recorded before the ex-date, an accrual once the
// ex-date has passed, the payment (with tax withheld at source) when it arrives.
// It reads declared payouts from the distribution observations; it fetches
// nothing and never places an order.
func cmdIncome(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: expected | accrue | receive | check")
	}
	switch args[0] {
	case "expected":
		return incomeExpected(ctx, a, args[1:])
	case "accrue":
		return incomeAccrue(ctx, a, args[1:])
	case "receive":
		return incomeReceive(ctx, a, args[1:])
	case "check":
		return incomeCheck(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (expected, accrue, receive, check)", args[0])
}

// incomeDay reads the -as-of day, defaulting to today.
func incomeDay(a *app, s string) (string, error) {
	if s == "" {
		return a.now().UTC().Format(time.DateOnly), nil
	}
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return "", usageErrorf("-as-of %q must be YYYY-MM-DD", s)
	}
	return s, nil
}

// bookPayouts are the resolved payouts of every security the book has ever
// held or ordered, as known at the time given (zero: everything received).
func bookPayouts(ctx context.Context, a *app, l *portfolio.Ledger, known time.Time) ([]distribution.Payout, error) {
	seen := map[string]bool{}
	var ids []string
	for _, tx := range l.Txs() {
		for _, e := range tx.Entries {
			if e.Security != "" && !seen[e.Security] {
				seen[e.Security] = true
				ids = append(ids, e.Security)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Strings(ids)
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	obs, err := db.DistributionObservations(ctx, ids, known)
	if err != nil {
		return nil, err
	}
	return distribution.Resolve(obs, time.Time{}), nil
}

func statusCell(s income.Status) cliCell {
	switch s {
	case income.Entitled:
		return styled(string(s), cliGreen)
	case income.Pending, income.NotEntitled, income.NoIncome:
		return styled(string(s), cliDim)
	}
	return styled(string(s), cliYellow)
}

func incomeExpected(ctx context.Context, a *app, args []string) error {
	fs := newFlags("income expected")
	asOf := fs.String("as-of", "", "the day to judge from, YYYY-MM-DD; default today")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: income expected BOOK [-as-of DAY] [-json]")
	}
	day, err := incomeDay(a, *asOf)
	if err != nil {
		return err
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	l, err := ps.Ledger(ctx, book)
	if err != nil {
		return err
	}
	payouts, err := bookPayouts(ctx, a, l, time.Time{})
	if err != nil {
		return err
	}
	var es []income.Expectation
	for _, p := range payouts {
		es = append(es, income.Expect(l, p, day))
	}
	if *asJSON {
		return writeJSON(a.stdout, es)
	}
	if len(es) == 0 {
		fmt.Fprintln(a.stdout, "no declared or estimated payout is known for the securities of this book\nimport some with: stoxicity dividends import")
		return nil
	}
	t := cliTable{Header: []string{"payout", "ex-date", "pay", "status", "shares", "per share", "gross", "booked", "reason"}, Right: []bool{false, false, false, false, true, true, true, false}}
	for _, e := range es {
		gross := "-"
		if e.Gross.Valid() {
			gross = e.Gross.String()
		}
		booked := "-"
		if st, ok := l.Projection().Payout(e.Ledger); ok {
			booked = amountText(st.Gross, st.Currency)
			if st.PaidTx != "" {
				booked += " paid"
			} else {
				booked += " accrued"
			}
		}
		shares := e.Shares.String()
		if e.Shares.Sign() == 0 {
			shares = "0"
		}
		t.Rows = append(t.Rows, []cliCell{plain(e.Ledger), plain(e.ExDay), plain(orDash(e.PayDay)), statusCell(e.Status), plain(shares), plain(e.PerShare.String() + " " + string(e.Currency)), plain(gross), plain(booked), plain(e.Reason)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout, "\ngross, before any tax withheld at source. Entitlement follows the fills recorded before the ex-date; an open order earns nothing. Nothing here is booked: use income accrue.")
	return nil
}

func incomeAccrue(ctx context.Context, a *app, args []string) error {
	fs := newFlags("income accrue")
	asOf := fs.String("as-of", "", "the day to judge from, YYYY-MM-DD; default today")
	only := fs.String("payout", "", "accrue only this payout key (security|type|period)")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: income accrue BOOK [-as-of DAY] [-payout KEY]")
	}
	day, err := incomeDay(a, *asOf)
	if err != nil {
		return err
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	var done, skipped []string
	err = ps.Update(ctx, []string{book}, func(now time.Time, ls []*portfolio.Ledger) (*store.ImportBatch, error) {
		l := ls[0]
		payouts, err := bookPayouts(ctx, a, l, time.Time{})
		if err != nil {
			return nil, err
		}
		for _, p := range payouts {
			e := income.Expect(l, p, day)
			if *only != "" && e.Ledger != *only {
				continue
			}
			if e.Status != income.Entitled {
				continue
			}
			if _, ok := l.Projection().Payout(e.Ledger); ok {
				skipped = append(skipped, e.Ledger+" (already booked)")
				continue
			}
			ex, err := time.Parse(time.DateOnly, e.ExDay)
			if err != nil {
				return nil, fmt.Errorf("payout %s has no usable ex-date %q", e.Ledger, e.ExDay)
			}
			tx, err := l.Accrue(portfolio.Common{Event: ex.UTC(), Source: "income-accrual", ImportID: "accrual:" + e.Ledger, Memo: fmt.Sprintf("%s shares x %s", e.Shares, e.PerShare)}, e.Ledger, e.Gross)
			if err != nil {
				return nil, err
			}
			if _, err := l.Post(tx, now); err != nil {
				return nil, fmt.Errorf("%s: %w", e.Ledger, err)
			}
			done = append(done, fmt.Sprintf("%s %s", e.Ledger, e.Gross))
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	for _, d := range done {
		fmt.Fprintf(a.stdout, "accrued %s\n", d)
	}
	for _, s := range skipped {
		fmt.Fprintf(a.stdout, "skipped %s\n", s)
	}
	if len(done) == 0 && len(skipped) == 0 {
		fmt.Fprintln(a.stdout, "nothing to accrue: no payout is entitled and past its ex-date")
	}
	return nil
}

func incomeReceive(ctx context.Context, a *app, args []string) error {
	fs := newFlags("income receive")
	key := fs.String("payout", "", "the payout key (security|type|period), as income expected shows it")
	date := fs.String("date", "", "the day the money or shares arrived, YYYY-MM-DD")
	withheld := fs.String("withheld", "", `tax withheld at source, e.g. "15.00 EUR"; empty means none`)
	gross := fs.String("gross", "", `gross amount, only for a payout that was never accrued, e.g. "100.00 EUR"`)
	security := fs.String("security", "", "with -qty: the payout was reinvested in this security")
	qty := fs.String("qty", "", "shares received by reinvestment")
	importID := fs.String("import-id", "", "the external ID of this record; repeating it changes nothing")
	memo := fs.String("memo", "", "a note")
	source := fs.String("source", "manual", "where the record came from")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *key == "" || *date == "" {
		return usageErrorf("usage: income receive BOOK -payout KEY -date D [-withheld AMOUNT] [-gross AMOUNT (not accrued)] [-security S -qty Q (reinvested)] [-import-id ID]")
	}
	ev, err := parseEvent(*date)
	if err != nil {
		return usageErrorf("%v", err)
	}
	var w, g money.Amount
	if *withheld != "" {
		if w, err = money.ParseAmount(*withheld); err != nil {
			return usageErrorf("-withheld: %v", err)
		}
	}
	if *gross != "" {
		if g, err = money.ParseAmount(*gross); err != nil {
			return usageErrorf("-gross: %v", err)
		}
	}
	var q money.Decimal
	if (*security == "") != (*qty == "") {
		return usageErrorf("-security and -qty go together")
	}
	if *qty != "" {
		if q, err = money.ParseDecimal(*qty); err != nil {
			return usageErrorf("-qty: %v", err)
		}
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	if *security != "" {
		if err := knownSecurity(secs, *security); err != nil {
			return err
		}
	}
	c := portfolio.Common{Event: ev, Source: *source, ImportID: *importID, Memo: *memo}
	var posted portfolio.Tx
	err = ps.Update(ctx, []string{book}, func(now time.Time, ls []*portfolio.Ledger) (*store.ImportBatch, error) {
		l := ls[0]
		req := strings.Join([]string{*key, *date, *withheld, *gross, *security, *qty}, "|")
		if c.ImportID != "" {
			if t, ok := l.Imported(c.ImportID); ok {
				if t.Meta["req"] != req {
					return nil, fmt.Errorf("%w: %q was recorded as %s with other content", portfolio.ErrImportConflict, c.ImportID, t.ID)
				}
				posted = t
				return nil, nil
			}
		}
		var tx portfolio.Tx
		var err error
		_, accrued := l.Projection().Payout(*key)
		switch {
		case *security != "":
			tx, err = l.ReinvestPayout(c, *key, *security, q, g, w)
		case accrued:
			if g.Valid() {
				return nil, errors.New("the payout is already accrued: its gross amount is the accrued one, so -gross is not accepted")
			}
			tx, err = l.Receive(c, *key, w)
		default:
			if !g.Valid() {
				return nil, fmt.Errorf("payout %q has not been accrued: run income accrue, or give -gross to book it directly", *key)
			}
			tx, err = l.ReceiveDirect(c, *key, g, w)
		}
		if err != nil {
			return nil, err
		}
		if tx.Meta == nil {
			tx.Meta = map[string]string{}
		}
		tx.Meta["req"] = req
		p, err := l.Post(tx, now)
		if err != nil {
			return nil, err
		}
		posted = p.Tx
		return nil, nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "recorded %s on %s: %s\n", posted.ID, posted.Event.Format(time.DateOnly), txEffect(posted))
	return nil
}

func incomeCheck(ctx context.Context, a *app, args []string) error {
	fs := newFlags("income check")
	asOf := fs.String("as-of", "", "the day to judge from, YYYY-MM-DD; default today")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: income check BOOK [-as-of DAY] [-json]")
	}
	day, err := incomeDay(a, *asOf)
	if err != nil {
		return err
	}
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	l, err := ps.Ledger(ctx, book)
	if err != nil {
		return err
	}
	payouts, err := bookPayouts(ctx, a, l, time.Time{})
	if err != nil {
		return err
	}
	fs2 := income.Check(l, payouts, day)
	if *asJSON {
		if err := writeJSON(a.stdout, fs2); err != nil {
			return err
		}
		if len(fs2) > 0 {
			return errReported
		}
		return nil
	}
	if len(fs2) == 0 {
		fmt.Fprintf(a.stdout, "the income booked agrees with the %d payouts known, as of %s\n", len(payouts), day)
		return nil
	}
	t := cliTable{Header: []string{"payout", "finding", "expected", "ledger", "why"}, Right: []bool{false, false, true, true}}
	for _, f := range fs2 {
		t.Rows = append(t.Rows, []cliCell{plain(f.Payout), styled(strings.ReplaceAll(string(f.Kind), "_", " "), cliRed), plain(f.Expected), plain(f.Ledger), plain(f.Reason)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintf(a.stdout, "\n%d difference(s). Nothing was changed: reverse the entry that is wrong (portfolio add BOOK reverse -ref ...) and book it again.\n", len(fs2))
	return errReported
}
