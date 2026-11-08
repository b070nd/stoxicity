package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/income"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/readiness"
	"github.com/b070nd/stoxicity/app/src/report"
)

// cmdReport exports what the program holds about one book, from stored records
// only: nothing is fetched, nothing is sent and no other book is opened.
func cmdReport(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: export BOOK [-as-of DAY] [-format md|json] [-out FILE] [-force]")
	}
	switch args[0] {
	case "export":
		return reportExport(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (export)", args[0])
}

type exportFlags struct {
	format string
	out    string
	force  bool
}

func (e exportFlags) check() error {
	if e.format != "md" && e.format != "json" {
		return usageErrorf("-format %q must be md or json", e.format)
	}
	return nil
}

// emit writes the rendered export to stdout or, with -out, to a file that is
// not replaced unless -force is given.
func (e exportFlags) emit(a *app, md func() string, js func() ([]byte, error)) error {
	var body []byte
	if e.format == "json" {
		raw, err := js()
		if err != nil {
			return err
		}
		body = append(raw, '\n')
	} else {
		body = []byte(md())
	}
	if e.out == "" {
		_, err := a.stdout.Write(body)
		return err
	}
	mode := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if e.force {
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(e.out, mode, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists: use -force to replace it", e.out)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "wrote %s (%d bytes)\n", e.out, len(body))
	return nil
}

func reportExport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("report export")
	asOf := fs.String("as-of", "", "the day to judge payouts and plans from, YYYY-MM-DD; default today")
	format := fs.String("format", "md", "md for a person, json for a program")
	out := fs.String("out", "", "write to this file instead of the screen; an existing file is kept")
	force := fs.Bool("force", false, "replace the file named by -out")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: report export BOOK [-as-of DAY] [-format md|json] [-out FILE] [-force]")
	}
	ef := exportFlags{*format, *out, *force}
	if err := ef.check(); err != nil {
		return err
	}
	day, err := incomeDay(a, *asOf)
	if err != nil {
		return err
	}
	r, err := buildReport(ctx, a, book, day)
	if err != nil {
		return err
	}
	return ef.emit(a, r.Markdown, r.JSON)
}

func buildReport(ctx context.Context, a *app, book, day string) (report.Report, error) {
	ps, err := a.portfolios()
	if err != nil {
		return report.Report{}, err
	}
	l, err := ps.Ledger(ctx, book)
	if err != nil {
		return report.Report{}, err
	}
	names, err := securitySet(ctx, a)
	if err != nil {
		return report.Report{}, err
	}
	v := viewOf(l, names)
	if !v.Balanced {
		return report.Report{}, errors.New("the books do not balance, so no report is made (run portfolio verify)")
	}
	r := report.Report{Program: "stoxicity", AsOf: day, Book: l.Book.Account + "/" + l.Book.Name, Account: l.Book.Account, Env: string(l.Book.Env), Base: string(l.Book.Base), Ledger: ledgerFingerprint(l)}
	r.Cash, r.Holdings, r.Reservations, r.Orders, r.Plans = []report.Cash{}, []report.Holding{}, []report.Reservation{}, []report.Order{}, []report.Plan{}
	for _, c := range v.Cash {
		r.Cash = append(r.Cash, report.Cash{Currency: string(c.Currency), Settled: c.Settled.String(), Unsettled: c.Unsettled.String(), Reserved: c.Reserved.String(), Available: c.Available.String()})
	}
	for _, p := range v.Positions {
		cost := ""
		for i, c := range p.Cost {
			if i > 0 {
				cost += " + "
			}
			cost += c.String()
		}
		r.Holdings = append(r.Holdings, report.Holding{Security: p.Security, Qty: p.Qty.String(), Cost: cost, Lots: p.Lots})
	}
	for _, x := range l.Projection().Reservations() {
		h := report.Reservation{ID: x.ID, Intent: x.Intent}
		if x.Kind == portfolio.ReserveCash {
			h.Kind, h.What = "cash", amountText(x.Amount, x.Currency)
		} else {
			h.Kind, h.What = "shares", x.Qty.String()+" "+x.Security
		}
		r.Reservations = append(r.Reservations, h)
	}

	// the policy and the tier in force
	db, err := a.store()
	if err != nil {
		return r, err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{})
	if err != nil {
		return r, err
	}
	r.Policy = "none active"
	if active {
		r.Tier, r.Policy = rec.Policy.Tier, fmt.Sprintf("epoch %d, hash %s", rec.Epoch, rec.Hash)
	}

	// the broker, described without its address, certificate or fixture file
	bc, ok, err := loadBrokerConfig(ctx, a)
	if err != nil {
		return r, err
	}
	if ok {
		r.Broker = report.Broker{Configured: true, Transport: bc.Transport, Mode: string(bc.Mode)}
	}

	// orders of this book only
	svc, err := orderService(ctx, a)
	if err != nil {
		return r, err
	}
	views, err := svc.Views(ctx, l.Book.Account)
	if err != nil {
		return r, err
	}
	for _, o := range views {
		if o.Terms.Book != l.Book.ID && o.Terms.Book != book {
			continue
		}
		r.Orders = append(r.Orders, report.Order{ID: o.ID, State: string(o.State), Env: string(o.Terms.Env), Side: string(o.Terms.Side), Security: o.Terms.Security, Qty: o.Terms.Qty.String(), Filled: o.Filled.String(), Limit: o.Terms.Limit.String(), Currency: string(o.Terms.Currency), Unknown: o.State == "unknown"})
	}

	// plans of this book
	repo, err := planRepo(a)
	if err != nil {
		return r, err
	}
	rvs, err := repo.Plans(ctx)
	if err != nil {
		return r, err
	}
	for _, rv := range rvs {
		if rv.Card.Book != l.Book.ID && rv.Card.Book != book {
			continue
		}
		st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return r, err
		}
		eff := plan.Effective(st, rv.Card.Expiry, a.now())
		c := rv.Card
		r.Plans = append(r.Plans, report.Plan{Revision: rv.ID(), Security: c.Security, Action: c.Action, State: string(eff), Expiry: c.Expiry, Data: c.DataState, Strategy: c.StrategyState, Execution: c.ExecutionEligibility, Reasons: nz(c.Reasons), Unknowns: nz(c.Unknowns), Manifest: rv.ManifestHash})
	}

	// income
	r.Income = report.Income{Payouts: []report.Payout{}, Findings: []string{}}
	payouts, err := bookPayouts(ctx, a, l, time.Time{})
	if err != nil {
		return r, err
	}
	for _, p := range payouts {
		e := income.Expect(l, p, day)
		gross := "-"
		if e.Gross.Valid() {
			gross = e.Gross.String()
		}
		r.Income.Payouts = append(r.Income.Payouts, report.Payout{Key: e.Ledger, Currency: string(e.Currency), Gross: gross, State: string(e.Status)})
	}
	for _, f := range income.Check(l, payouts, day) {
		r.Income.Findings = append(r.Income.Findings, fmt.Sprintf("%s: %s (expected %s, ledger %s)", f.Payout, f.Reason, f.Expected, f.Ledger))
	}
	r.Income.Note = "Payouts are gross, before any tax withheld at source."

	// what the product claims about itself
	for _, c := range readiness.Inventory() {
		r.Capabilities = append(r.Capabilities, report.Capability{ID: c.ID, Name: c.Name,
			Implemented: string(c.Level(readiness.Implemented)), Fixture: string(c.Level(readiness.FixtureVerified)),
			Source: string(c.Level(readiness.SourceVerified)), Account: string(c.Level(readiness.AccountVerified)), Investment: string(c.Level(readiness.InvestmentEvaluated))})
	}
	r.Omitted = []string{
		"No article text, headline or snippet of any source: plans refer to evidence by identifier, so a source licence that forbids redistribution is respected.",
		"No broker address, certificate path, fixture path, account identifier from a broker, API key or token.",
		"No other book of this tenant, and no price or exchange-rate data: nothing was fetched to make this report.",
		"No valuation of the holdings: a value would need prices, and the report does not fetch them.",
	}
	sort.Slice(r.Plans, func(i, j int) bool { return r.Plans[i].Revision < r.Plans[j].Revision })
	return r, nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ledgerFingerprint identifies the exact content of a ledger: the hash of the
// stored content hashes of its transactions in order.
func ledgerFingerprint(l *portfolio.Ledger) string {
	h := sha256.New()
	for _, tx := range l.Txs() {
		h.Write([]byte(l.Hash(tx.ID)))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func planExport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan export")
	format := fs.String("format", "md", "md for a person, json for a program")
	out := fs.String("out", "", "write to this file instead of the screen; an existing file is kept")
	force := fs.Bool("force", false, "replace the file named by -out")
	ref, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if ref == "" {
		return usageErrorf("usage: plan export PLAN[.rN] [-format md|json] [-out FILE] [-force]")
	}
	ef := exportFlags{*format, *out, *force}
	if err := ef.check(); err != nil {
		return err
	}
	id, n, err := planRef(ref)
	if err != nil {
		return err
	}
	repo, err := planRepo(a)
	if err != nil {
		return err
	}
	rv, err := repo.Get(ctx, id, n)
	if err != nil {
		return err
	}
	st, log, err := repo.State(ctx, rv.PlanID, rv.Rev)
	if err != nil {
		return err
	}
	d := report.PlanDoc{Program: "stoxicity", Revision: rv, State: string(st), Effective: string(plan.Effective(st, rv.Card.Expiry, a.now())), Intact: rv.Verify() == nil, Log: log}
	if log == nil {
		d.Log = []plan.Event{}
	}
	return ef.emit(a, d.Markdown, d.JSON)
}
