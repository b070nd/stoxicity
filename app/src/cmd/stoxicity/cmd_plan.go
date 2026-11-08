package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/allocate"
	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/strategy"
)

// cmdPlan stores recommendations as immutable snapshots with a lifecycle. A plan
// is a record of a view and of the user's decision about it; it places nothing.
func cmdPlan(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: generate | list | show | diff | check | propose | accept | reject | expire")
	}
	switch args[0] {
	case "generate":
		return planGenerate(ctx, a, args[1:])
	case "list":
		return planList(ctx, a, args[1:])
	case "show":
		return planShow(ctx, a, args[1:])
	case "diff":
		return planDiff(ctx, a, args[1:])
	case "check":
		return planCheck(ctx, a, args[1:])
	case "propose":
		return planMove(ctx, a, plan.Proposed, args[1:])
	case "accept":
		return planMove(ctx, a, plan.Accepted, args[1:])
	case "reject":
		return planMove(ctx, a, plan.Rejected, args[1:])
	case "expire":
		return planExpire(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (generate, list, show, diff, check, propose, accept, reject, expire)", args[0])
}

func planRepo(a *app) (plan.Repo, error) {
	rs, err := a.records()
	if err != nil {
		return plan.Repo{}, err
	}
	return plan.NewRepo(rs), nil
}

func textHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func ratesText(r map[money.Currency]money.Decimal) string {
	var ks []string
	for c, d := range r {
		ks = append(ks, fmt.Sprintf("%s=%s", c, d))
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func pcOf(num, den int64) string {
	if den <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.2f%%", float64(num)*100/float64(den))
}

// buildCards turns the gathered allocation into one card and manifest per security.
func buildCards(gt *allocGathered, day, expiry string) ([]plan.Card, []plan.Manifest, error) {
	lines := map[string]allocate.Line{}
	for _, l := range gt.Plan.Lines {
		lines[l.Security] = l
	}
	skip := map[string]string{}
	for _, s := range append(append([]allocate.Skip{}, gt.Skipped...), gt.Plan.Skipped...) {
		if s.Security != "" && s.Security != "*" {
			skip[s.Security] = s.Reason
		}
	}
	ids := map[string]bool{}
	for id := range gt.Proposals {
		ids[id] = true
	}
	for id := range gt.Funds {
		ids[id] = true
	}
	for id := range skip {
		ids[id] = true
	}
	for _, c := range gt.Req.Candidates {
		ids[c.Security] = true
	}
	order := make([]string, 0, len(ids))
	for id := range ids {
		order = append(order, id)
	}
	sort.Strings(order)

	costs := map[string]string{"spread": "unavailable: no quote spread is stored", "tax": "unavailable: tax residence and treatment are not stated"}
	if gt.Req.Fees.Known {
		costs["commission_and_fx"] = "per the schedule: " + gt.Req.Fees.Source
	} else {
		costs["commission_and_fx"] = "unavailable: no sourced fee schedule was supplied"
	}
	equity := gt.Plan.Equity
	holdingByIssuer := map[string]int64{}
	for _, h := range gt.Req.Holdings {
		if h.ValueKnown {
			holdingByIssuer[h.Issuer] += h.Value
		}
	}
	candByID := map[string]allocate.Candidate{}
	for _, c := range gt.Req.Candidates {
		candByID[c.Security] = c
	}
	var cards []plan.Card
	var mans []plan.Manifest
	pol := gt.Policy
	for _, id := range order {
		sec := gt.Names[id]
		card := plan.Card{AsOf: day, Expiry: expiry, Security: id, Name: sec.Name, Book: gt.BookID, Sleeve: "core",
			Costs: costs, Held: gt.HeldQty[id], Free: gt.FreeQty[id], ExecutionEligibility: "research_only",
			WouldChange: []string{"a new filing, a changed assumption, a new price or a policy change regenerates this plan as a new revision"},
			ExitRules: []string{
				"a review date is not a sale date: an overdue review is shown as overdue, never as a fresh HOLD",
				"a dividend cut triggers a thesis review, not an unconditional sale",
				"nothing is sold to make room: contributions are used first",
			}}
		man := plan.Manifest{AsOf: day, PolicyEpoch: pol.Epoch, Policy: pol.Hash, Fees: textHash(gt.Req.Fees), Rates: ratesText(gt.Req.Rates), Ledger: gt.Ledger}
		line, allocated := lines[id]
		reason := skip[id]
		if p := gt.Proposals[id]; p != nil {
			card.Strategy, card.Action, card.DataState, card.StrategyState, card.ExecutionEligibility = p.Strategy, string(p.Action), p.DataState, p.StrategyState, p.ExecutionEligibility
			card.Reasons = append(card.Reasons, p.Reasons...)
			card.Thesis, card.Counter, card.Invalidation = p.Thesis, p.Counter, p.Invalidation
			card.Score, card.MaxScore, card.Coverage = p.Score, p.MaxScore, p.Coverage
			card.IncomePreference, card.NextExDay, card.ReviewBy = p.IncomePreference, p.NextExDay, p.ReviewBy
			card.Horizon = "core sleeve: a thesis horizon of years; review quarterly and on filings or distribution changes"
			for _, t := range p.Tests {
				card.Components = append(card.Components, plan.Component{Group: t.Group, ID: t.ID, Result: string(t.Result), Required: t.Required, Awarded: t.Awarded, Max: t.Max, Detail: t.Detail})
				if t.Result == strategy.Unknown {
					card.Unknowns = append(card.Unknowns, t.ID+": "+t.Detail)
				}
				if (t.Result == strategy.Fail || t.Result == strategy.Unknown) && t.Required {
					card.WouldChange = append(card.WouldChange, "if the required test "+t.ID+" passed")
				}
			}
			for _, alt := range p.Alternatives {
				card.Alternatives = append(card.Alternatives, alt.Name+": "+alt.Note)
			}
			if v := p.Valuation; v != nil {
				card.Scenarios = &plan.Scenarios{Currency: string(v.Currency), Bear: v.Bear.String(), Base: v.Base.String(), Bull: v.Bull.String(), Ceiling: v.Ceiling.String(), Entry: v.EntryScenario, Margin: v.MarginOfSafety.String(), Assumptions: p.Inputs.Assumptions}
			}
			man.Strategy, man.Config, man.Scorecard, man.Assumptions, man.Price, man.Payouts = p.Strategy, p.Inputs.Config, p.Inputs.Card, p.Inputs.Assumptions, p.Inputs.Price, p.Inputs.Payouts
		} else if fa, ok := gt.Funds[id]; ok {
			card.Strategy, card.Action, card.DataState, card.StrategyState = fa.Strategy, "WATCH", "documented", "default fund checks"
			card.Reasons = append(card.Reasons, fa.Reasons...)
			card.Horizon = "core sleeve: a broad fund held for years; no price target"
			card.ReviewBy = ""
			card.Alternatives = []string{"individual stocks: more concentrated, higher cost per order at this size", "cash and contributions: no market risk, no income"}
			for _, ck := range fa.Checks {
				card.Components = append(card.Components, plan.Component{Group: "fund", ID: ck.ID, Result: string(ck.Result), Detail: ck.Detail})
				if ck.Result == strategy.Unknown {
					card.Unknowns = append(card.Unknowns, ck.ID+": "+ck.Detail)
				}
			}
			card.Invalidation = []string{"the fund's documented costs, structure or domicile change", "a better broad fund of the same kind is documented"}
			man.Strategy, man.Config, man.Scorecard = fa.Strategy, textHash(strategy.DefaultFundConfig()), textHash(fa)
			if fa.Eligible && fa.Broad {
				card.Action = "BUY"
				if gt.HeldQty[id] != "" && gt.HeldQty[id] != "0" {
					card.Action = "ADD"
				}
			}
		} else {
			card.Strategy, card.Action, card.DataState, card.StrategyState = "unassessed", "ABSTAIN", "incomplete", "not assessed"
			card.Invalidation = []string{"not applicable: nothing is proposed"}
			man.Strategy = "unassessed"
		}
		if card.Action == "BUY" || card.Action == "ADD" {
			if allocated {
				c := candByID[id]
				cur := string(c.Currency)
				entry := ""
				if !c.Ceiling.IsZero() {
					entry = c.Ceiling.String()
				}
				card.Sizing = &plan.Sizing{Qty: line.Qty, Reference: c.Price.String(), EntryMax: entry, PriceCurrency: cur, Base: string(gt.Base), Notional: line.Notional, Fee: line.Fee, Burden: line.Burden,
					CashBefore: gt.Req.Cash, CashAfter: gt.Plan.CashAfter, Reserve: gt.Req.Limits.CashReserve.String(), Binding: line.Note}
				card.Concentration = []plan.Concentration{{Dimension: "issuer", Name: c.Issuer, Before: pcOf(holdingByIssuer[c.Issuer], equity), After: pcOf(holdingByIssuer[c.Issuer]+line.Notional, equity)}}
				card.Reasons = append(card.Reasons, "allocated: "+line.Note)
			} else {
				card.Action = "WAIT"
				if reason == "" {
					reason = "the allocator proposed no purchase for it"
					if len(gt.Plan.Explanation) > 0 {
						reason = gt.Plan.Explanation[0]
					}
				}
				card.Reasons = append(card.Reasons, "not allocated: "+reason)
				card.WouldChange = append(card.WouldChange, "more cash, lower costs or a cheaper price")
			}
		} else if reason != "" {
			card.Reasons = append(card.Reasons, "allocator: "+reason)
		}
		card.Unknowns = append(card.Unknowns, gt.Plan.Unknown...)
		cards = append(cards, card)
		mans = append(mans, man)
	}
	return cards, mans, nil
}

func planGenerate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan generate")
	candFile := fs.String("candidates", "", "CSV of the securities to consider (allocate example)")
	feeFile := fs.String("fees", "", "JSON fee schedule with its source")
	asOf := fs.String("as-of", "", "the day, YYYY-MM-DD; default today. Policy, book and evidence are read as they were then")
	contrib := fs.String("contribution", "", `the regular contribution, e.g. "50.00 EUR"`)
	rates := fs.String("rates", "", "base units per one unit of a foreign currency, e.g. USD=0.92")
	valid := fs.Int("valid-days", 3, "days after the as-of day the plan stays valid")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" || *candFile == "" {
		return usageErrorf("usage: plan generate BOOK -candidates FILE -fees FILE [-as-of DAY] [-contribution A] [-rates USD=0.92] [-valid-days N] [-json]")
	}
	if *valid < 1 || *valid > 30 {
		return usageErrorf("-valid-days must be 1-30")
	}
	day := a.now().UTC().Format(time.DateOnly)
	pin := time.Time{}
	if *asOf != "" {
		day = *asOf
	}
	dayT, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return usageErrorf("-as-of must be YYYY-MM-DD")
	}
	if *asOf != "" {
		pin = dayT.Add(24*time.Hour - time.Nanosecond)
	}
	gt, err := gatherAllocation(ctx, a, allocParams{Book: book, CandFile: *candFile, FeeFile: *feeFile, Rates: *rates, Contribution: *contrib, Day: day, PolicyAt: pin, LedgerAt: pin})
	if err != nil {
		return err
	}
	gt.Plan = allocate.Allocate(gt.Req)
	expiry := dayT.AddDate(0, 0, *valid).Format(time.DateOnly)
	cards, mans, err := buildCards(gt, day, expiry)
	if err != nil {
		return err
	}
	repo, err := planRepo(a)
	if err != nil {
		return err
	}
	type row struct {
		ID, Security, Action, Qty, Outcome string
		Rev                                int
		Material                           bool
	}
	var rows []row
	for i, c := range cards {
		rev, err := plan.New(c, mans[i], a.now())
		if err != nil {
			return fmt.Errorf("%s: %w", c.Security, err)
		}
		sv, err := repo.Save(ctx, rev, a.now())
		if err != nil {
			return err
		}
		out := "new"
		switch {
		case !sv.Created:
			out = "unchanged"
		case sv.Revision.Rev > 1 && sv.Revision.Material():
			out = "revised (material)"
		case sv.Revision.Rev > 1:
			out = "revised (minor)"
		}
		if sv.Superseded != "" {
			out += "; superseded " + sv.Superseded
		}
		if sv.Accepted != "" {
			out += "; " + sv.Accepted + " stays accepted until you accept this"
		}
		qty := ""
		if c.Sizing != nil {
			qty = c.Sizing.Qty
		}
		rows = append(rows, row{sv.Revision.PlanID, c.Security, c.Action, qty, out, sv.Revision.Rev, sv.Revision.Material()})
	}
	if *asJSON {
		return printJSON(a, rows)
	}
	t := cliTable{Header: []string{"plan", "rev", "security", "action", "quantity", "result"}, Right: []bool{false, true, false, false, true}}
	for _, r := range rows {
		t.Rows = append(t.Rows, []cliCell{styled(r.ID, cliStrong), plain(fmt.Sprint(r.Rev)), plain(r.Security), actionCell(strategy.Action(r.Action)), plain(r.Qty), plain(r.Outcome)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintf(a.stdout, "\nvalid through %s. A plan is a record, not an authorization: nothing is placed, and acceptance still needs its own order preflight.\n", expiry)
	return nil
}

func planRef(s string) (string, int, error) {
	id, rv, ok := strings.Cut(s, ".r")
	if !ok {
		return s, 0, nil
	}
	var n int
	if _, err := fmt.Sscanf(rv, "%d", &n); err != nil || n < 1 {
		return "", 0, usageErrorf("%q is not a plan reference (pl_xxxx or pl_xxxx.r2)", s)
	}
	return id, n, nil
}

func planStateCell(s plan.State) cliCell {
	switch s {
	case plan.Accepted:
		return styled(string(s), cliGreen)
	case plan.Rejected, plan.Expired, plan.Superseded:
		return styled(string(s), cliDim)
	case plan.Proposed:
		return styled(string(s), cliYellow)
	}
	return plain(string(s))
}

func planList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan list")
	all := fs.Bool("all", false, "include rejected, expired and superseded plans")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	repo, err := planRepo(a)
	if err != nil {
		return err
	}
	ps, err := repo.Plans(ctx)
	if err != nil {
		return err
	}
	type row struct {
		Plan, Security, Action, State, Qty, Expiry string
		Rev                                        int
	}
	var rows []row
	for _, rv := range ps {
		st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return err
		}
		eff := plan.Effective(st, rv.Card.Expiry, a.now())
		if !*all && eff.Terminal() {
			continue
		}
		q := ""
		if rv.Card.Sizing != nil {
			q = rv.Card.Sizing.Qty
		}
		rows = append(rows, row{rv.PlanID, rv.Card.Security, rv.Card.Action, string(eff), q, rv.Card.Expiry, rv.Rev})
	}
	if *asJSON {
		return printJSON(a, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, "no open plans (plan generate; -all shows settled ones)")
		return nil
	}
	t := cliTable{Header: []string{"plan", "rev", "security", "action", "state", "quantity", "expires"}, Right: []bool{false, true, false, false, false, true}}
	for _, r := range rows {
		t.Rows = append(t.Rows, []cliCell{styled(r.Plan, cliStrong), plain(fmt.Sprint(r.Rev)), plain(r.Security), actionCell(strategy.Action(r.Action)), planStateCell(plan.State(r.State)), plain(r.Qty), plain(r.Expiry)})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func writeList(a *app, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(a.stdout, "\n%s:\n", title)
	for _, l := range lines {
		fmt.Fprintln(a.stdout, "  - "+l)
	}
}

func planShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan show")
	asJSON := fs.Bool("json", false, "print as JSON")
	ref, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if ref == "" {
		return usageErrorf("usage: plan show PLAN[.rN] [-json]")
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
	if *asJSON {
		return printJSON(a, map[string]any{"revision": rv, "state": st, "log": log, "intact": rv.Verify() == nil})
	}
	c := rv.Card
	eff := plan.Effective(st, c.Expiry, a.now())
	fmt.Fprintf(a.stdout, "%s  %s  %s %s  %s\n", styledText(rv.ID(), cliStrong, useColor(a.stdout)), c.Security, styledText(c.Action, actionCell(strategy.Action(c.Action)).Style, useColor(a.stdout)), c.Name, styledText(string(eff), planStateCell(eff).Style, useColor(a.stdout)))
	if eff != st {
		fmt.Fprintf(a.stdout, "recorded as %s; past its expiry day (plan expire records it)\n", st)
	}
	fmt.Fprintf(a.stdout, "book %s, sleeve %s, strategy %s; as of %s, generated %s, expires %s\n", c.Book, c.Sleeve, c.Strategy, c.AsOf, rv.Generated.Format(time.RFC3339), c.Expiry)
	fmt.Fprintf(a.stdout, "data %s, strategy %s, execution %s; score %d of %d (a policy ordering, not a probability), coverage %s\n", c.DataState, c.StrategyState, c.ExecutionEligibility, c.Score, c.MaxScore, orDash(c.Coverage))
	if s := c.Sizing; s != nil {
		fmt.Fprintf(a.stdout, "\nquantity %s sized at %s %s, entry at most %s; notional %s, fee %s (%s), cash %s -> %s, reserve %s\n", s.Qty, s.Reference, s.PriceCurrency, orDash(s.EntryMax), minorText(s.Notional, money.Currency(s.Base)), minorText(s.Fee, money.Currency(s.Base)), orDash(s.Burden), minorText(s.CashBefore, money.Currency(s.Base)), minorText(s.CashAfter, money.Currency(s.Base)), s.Reserve)
		if s.Binding != "" {
			fmt.Fprintln(a.stdout, "sized: "+s.Binding)
		}
	}
	if v := c.Scenarios; v != nil {
		fmt.Fprintf(a.stdout, "\nscenarios per share in %s (assumptions, not targets): bear %s, base %s, bull %s; entry ceiling %s (%s less %s margin of safety)\n", v.Currency, v.Bear, v.Base, v.Bull, v.Ceiling, v.Entry, v.Margin)
	}
	if len(c.Concentration) > 0 {
		t := cliTable{Header: []string{"exposure", "name", "before", "after"}, Right: []bool{false, false, true, true}}
		for _, x := range c.Concentration {
			t.Rows = append(t.Rows, []cliCell{plain(x.Dimension), plain(x.Name), plain(x.Before), plain(x.After)})
		}
		fmt.Fprintln(a.stdout)
		t.write(a.stdout, useColor(a.stdout))
	}
	keys := make([]string, 0, len(c.Costs))
	for k := range c.Costs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var cs []string
	for _, k := range keys {
		cs = append(cs, k+": "+c.Costs[k])
	}
	writeList(a, "costs and assumptions", cs)
	writeList(a, "reasons", c.Reasons)
	writeList(a, "unknown", c.Unknowns)
	writeList(a, "thesis (supporting notes)", c.Thesis)
	writeList(a, "counter-evidence", c.Counter)
	writeList(a, "invalidation", c.Invalidation)
	writeList(a, "reduce and sell rules", c.ExitRules)
	writeList(a, "alternatives", c.Alternatives)
	writeList(a, "what would change this", c.WouldChange)
	if c.Horizon != "" {
		fmt.Fprintf(a.stdout, "\nhorizon: %s; next review %s\n", c.Horizon, orDash(c.ReviewBy))
	}
	if c.IncomePreference {
		fmt.Fprintf(a.stdout, "income preference: yes (a tie-breaker, no points); next ex-date %s\n", orDash(c.NextExDay))
	}
	fmt.Fprintf(a.stdout, "\ninputs: policy epoch %d, config %s, scorecard %s, assumptions %s, price %s; manifest %s\n", rv.Manifest.PolicyEpoch, trunc(rv.Manifest.Config, 10), orDash(trunc(rv.Manifest.Scorecard, 10)), orDash(trunc(rv.Manifest.Assumptions, 10)), orDash(rv.Manifest.Price), rv.ManifestHash[:16])
	if err := rv.Verify(); err != nil {
		fmt.Fprintln(a.stdout, styledText("INTEGRITY: "+err.Error(), cliRed, useColor(a.stdout)))
	}
	if len(log) > 0 {
		fmt.Fprintln(a.stdout, "\nlifecycle:")
		for _, e := range log {
			fmt.Fprintf(a.stdout, "  %s  %s -> %s by %s %s\n", e.At.Format(time.RFC3339), e.From, e.To, e.By, e.Note)
		}
	}
	return nil
}

func planDiff(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan diff")
	from := fs.Int("from", 0, "the older revision; default the one before the newest")
	to := fs.Int("to", 0, "the newer revision; default the newest")
	asJSON := fs.Bool("json", false, "print as JSON")
	ref, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if ref == "" {
		return usageErrorf("usage: plan diff PLAN [-from N] [-to M] [-json]")
	}
	id, _, err := planRef(ref)
	if err != nil {
		return err
	}
	repo, err := planRepo(a)
	if err != nil {
		return err
	}
	all, err := repo.Revisions(ctx, id)
	if err != nil {
		return err
	}
	b := all[len(all)-1]
	if *to != 0 {
		if b, err = repo.Get(ctx, id, *to); err != nil {
			return err
		}
	}
	if *from == 0 {
		*from = b.Rev - 1
	}
	if *from < 1 {
		return errors.New("this plan has only one revision: nothing to compare")
	}
	old, err := repo.Get(ctx, id, *from)
	if err != nil {
		return err
	}
	ch := plan.Diff(old.Card, b.Card)
	inputs := old.Manifest.Changed(b.Manifest)
	if *asJSON {
		return printJSON(a, map[string]any{"from": old.Rev, "to": b.Rev, "changes": ch, "inputs_changed": inputs})
	}
	fmt.Fprintf(a.stdout, "%s: revision %d -> %d\n", id, old.Rev, b.Rev)
	if len(ch) == 0 {
		fmt.Fprintln(a.stdout, "the cards are identical")
	} else {
		t := cliTable{Header: []string{"what", "material", "from", "to"}}
		for _, c := range ch {
			m := plain("no")
			if c.Material {
				m = styled("yes", cliYellow)
			}
			t.Rows = append(t.Rows, []cliCell{plain(c.Field), m, plain(c.From), plain(c.To)})
		}
		t.write(a.stdout, useColor(a.stdout))
	}
	fmt.Fprintf(a.stdout, "inputs changed: %s\n", orDash(strings.Join(inputs, ", ")))
	return nil
}

// currentManifest recomputes, from today's stored inputs, the part of a
// revision's manifest that can be recomputed without the files a run was given.
func currentManifest(ctx context.Context, a *app, rv plan.Revision) (plan.Manifest, error) {
	cur := rv.Manifest
	db, err := a.store()
	if err != nil {
		return cur, err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{})
	if err != nil {
		return cur, err
	}
	if !active {
		cur.Policy, cur.PolicyEpoch = "none", 0
	} else {
		cur.Policy, cur.PolicyEpoch = rec.Hash, rec.Epoch
	}
	ps, err := a.portfolios()
	if err != nil {
		return cur, err
	}
	l, err := ps.Ledger(ctx, rv.Card.Book)
	if err != nil {
		return cur, err
	}
	cur.Ledger = l.Projection().Fingerprint()
	if rv.Card.Strategy != strategy.CoreID {
		return cur, nil // a fund's facts are a file the run was given; its expiry covers it
	}
	rs, err := a.records()
	if err != nil {
		return cur, err
	}
	cfg, _, err := loadConfig(ctx, rs)
	if err != nil {
		return cur, err
	}
	cur.Config = cfg.Hash()
	// The scorecard is read as of the plan's own day, so an unchanged scorecard
	// has the same hash. Anything recorded after the plan was generated, even if
	// dated later, is information the plan did not have.
	dayT, err := time.Parse(time.DateOnly, rv.Card.AsOf)
	if err != nil {
		return cur, err
	}
	known := dayT.Add(24*time.Hour - time.Nanosecond)
	card, err := loadCard(ctx, rs, rv.Card.Security, known, 5)
	if err != nil {
		return cur, err
	}
	cur.Scorecard = card.Hash
	newer := 0
	for _, kind := range []string{recDatum, recNote} {
		recs, err := rs.All(ctx, kind, rv.Card.Security+"|")
		if err != nil {
			return cur, err
		}
		for _, r := range recs {
			if r.At.After(rv.Generated) {
				newer++
			}
		}
	}
	if newer > 0 {
		cur.Scorecard += fmt.Sprintf("|%d figure or note record(s) written after generation", newer)
	}
	as, err := loadAssumptions(ctx, rs, rv.Card.Security)
	if err != nil {
		return cur, err
	}
	cur.Assumptions = ""
	if as != nil {
		cur.Assumptions = as.Hash()
	}
	seen := a.now()
	if known.After(seen) {
		seen = known
	}
	obs, err := db.DistributionObservations(ctx, []string{rv.Card.Security}, seen)
	if err != nil {
		return cur, err
	}
	cur.Payouts = len(distribution.Resolve(obs, time.Time{}))
	return cur, nil
}

func planCheck(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan check")
	ref, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if ref == "" {
		return usageErrorf("usage: plan check PLAN[.rN]")
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
	cur, err := currentManifest(ctx, a, rv)
	if err != nil {
		return err
	}
	st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
	if err != nil {
		return err
	}
	if err := rv.Verify(); err != nil {
		return err
	}
	ch := rv.Manifest.Changed(cur)
	eff := plan.Effective(st, rv.Card.Expiry, a.now())
	switch {
	case len(ch) > 0:
		fmt.Fprintf(a.stdout, "%s is %s and STALE: %s changed since it was generated. It stays readable and unchanged; regenerate it (plan generate) to get a new revision.\n", rv.ID(), eff, strings.Join(ch, ", "))
	default:
		fmt.Fprintf(a.stdout, "%s is %s and its recomputable inputs are unchanged (policy, strategy configuration, scorecard, assumptions, dividend observations, book). The price, fee schedule and rates came from files and are covered by the expiry %s.\n", rv.ID(), eff, rv.Card.Expiry)
	}
	return nil
}

func planMove(ctx context.Context, a *app, to plan.State, args []string) error {
	fs := newFlags("plan " + map[plan.State]string{plan.Proposed: "propose", plan.Accepted: "accept", plan.Rejected: "reject"}[to])
	by := fs.String("by", "", "who is deciding")
	note := fs.String("note", "", "why (required to reject)")
	ref, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if ref == "" {
		return usageErrorf("usage: plan propose|accept|reject PLAN[.rN] -by NAME [-note TEXT]")
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
	cur, err := currentManifest(ctx, a, rv)
	if err != nil {
		return err
	}
	ev, err := repo.Move(ctx, rv, to, *by, *note, cur, a.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s: %s -> %s by %s\n", rv.ID(), ev.From, ev.To, ev.By)
	if to == plan.Accepted {
		fmt.Fprintln(a.stdout, "accepted is your decision on record. It is not an order and not an authorization: an order needs its own preflight and confirmation.")
	}
	return nil
}

func planExpire(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan expire")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	repo, err := planRepo(a)
	if err != nil {
		return err
	}
	ps, err := repo.Plans(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, latest := range ps {
		all, err := repo.Revisions(ctx, latest.PlanID)
		if err != nil {
			return err
		}
		for _, rv := range all {
			st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
			if err != nil {
				return err
			}
			if st.Terminal() || plan.Effective(st, rv.Card.Expiry, a.now()) != plan.Expired {
				continue
			}
			if _, err := repo.Move(ctx, rv, plan.Expired, "system", "past its expiry day "+rv.Card.Expiry, rv.Manifest, a.now()); err != nil {
				return err
			}
			n++
		}
	}
	fmt.Fprintf(a.stdout, "recorded %d expired revision(s)\n", n)
	return nil
}
