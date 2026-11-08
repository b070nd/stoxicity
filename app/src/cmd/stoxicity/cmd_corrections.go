package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/allocate"
	"github.com/b070nd/stoxicity/app/src/corrections"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/plan"
)

// cmdCorrections keeps plans honest about the evidence they cite. A scan
// compares every live plan with the versions of the events it rests on, the
// thesis rules you recorded and the figures it used; a material change makes a
// new revision with its explanation, withdraws the confirmations of orders not
// yet sent, and raises one alert. It never touches an order that was sent.
func cmdCorrections(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: scan | list | show | rule")
	}
	switch args[0] {
	case "scan":
		return correctionsScan(ctx, a, args[1:])
	case "list":
		return correctionsList(ctx, a, args[1:])
	case "show":
		return correctionsShow(ctx, a, args[1:])
	case "rule":
		return correctionsRule(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (scan, list, show, rule)", args[0])
}

func correctionsRepo(a *app) (corrections.Repo, error) {
	rs, err := a.records()
	if err != nil {
		return corrections.Repo{}, err
	}
	return corrections.Repo{RS: rs}, nil
}

// securityTickers are the tickers the evidence history knows the security by.
func securityTickers(ctx context.Context, a *app, security string) ([]string, error) {
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	set, err := db.InstrumentsAsOf(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range set.Listings {
		if l.SecurityID == security && l.Ticker != "" && !seen[l.Ticker] {
			seen[l.Ticker] = true
			out = append(out, l.Ticker)
		}
	}
	sort.Strings(out)
	return out, nil
}

// evidenceOf reads the events about a security now: all of them, and the ones
// the plan rests on.
func evidenceOf(ctx context.Context, a *app, security string) (all, supporting []corrections.EventPin, tickers []string, err error) {
	if tickers, err = securityTickers(ctx, a, security); err != nil || len(tickers) == 0 {
		return nil, nil, tickers, err
	}
	db, err := a.store()
	if err != nil {
		return nil, nil, nil, err
	}
	if all, err = corrections.Collect(ctx, db, tickers, a.now()); err != nil {
		return nil, nil, nil, err
	}
	return all, corrections.Supporting(all), tickers, nil
}

// depsOf names what a revision depends on, as the evidence stands now.
func depsOf(ctx context.Context, a *app, rv plan.Revision, all, supporting []corrections.EventPin, tickers []string) (corrections.Deps, error) {
	cr, err := correctionsRepo(a)
	if err != nil {
		return corrections.Deps{}, err
	}
	rules, err := cr.Rules(ctx, rv.Card.Security)
	if err != nil {
		return corrections.Deps{}, err
	}
	d := corrections.Deps{Plan: rv.PlanID, Rev: rv.Rev, Security: rv.Card.Security, Tickers: tickers, Events: supporting, RuleHits: map[string][]string{}, Recorded: a.now().UTC()}
	for _, r := range rules {
		if r.Retired {
			continue
		}
		d.Rules = append(d.Rules, corrections.RulePin{ID: r.ID, Version: r.Version})
		if m := r.Matches(all); len(m) > 0 {
			d.RuleHits[r.ID] = m
		}
	}
	return d, nil
}

// withEvidence puts the cited events on a card and their hash on its manifest.
func withEvidence(ctx context.Context, a *app, c *plan.Card, m *plan.Manifest) error {
	_, sup, _, err := evidenceOf(ctx, a, c.Security)
	if err != nil {
		return err
	}
	c.Evidence = corrections.Lines(sup)
	m.Evidence = corrections.Hash(sup)
	return nil
}

// trackRevision records what a saved revision depends on, so the scan can see
// when it changes.
func trackRevision(ctx context.Context, a *app, rv plan.Revision) error {
	cr, err := correctionsRepo(a)
	if err != nil {
		return err
	}
	// The dependencies of a revision are written once, when it is made. Reading
	// them again later would hide a change that happened in between.
	if _, ok, err := cr.Deps(ctx, rv.PlanID, rv.Rev); err != nil || ok {
		return err
	}
	all, sup, tickers, err := evidenceOf(ctx, a, rv.Card.Security)
	if err != nil {
		return err
	}
	d, err := depsOf(ctx, a, rv, all, sup, tickers)
	if err != nil {
		return err
	}
	return cr.PutDeps(ctx, d)
}

// currentEvidenceHash is the hash of the events a revision cites as they stand
// now; a revision made before dependencies were kept keeps its own.
func currentEvidenceHash(ctx context.Context, a *app, rv plan.Revision) (string, error) {
	cr, err := correctionsRepo(a)
	if err != nil {
		return "", err
	}
	d, ok, err := cr.Deps(ctx, rv.PlanID, rv.Rev)
	if err != nil {
		return "", err
	}
	if !ok {
		return rv.Manifest.Evidence, nil
	}
	db, err := a.store()
	if err != nil {
		return "", err
	}
	return corrections.CurrentHash(ctx, db, d.Events)
}

func fileHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8]), nil
}

// recordRecipe keeps how a plan was generated so a correction can generate it
// again without anyone typing the command.
func recordRecipe(ctx context.Context, a *app, rv plan.Revision, book, candFile, feeFile, rates, contribution string, validDays int) error {
	rc := corrections.Recipe{Plan: rv.PlanID, Rev: rv.Rev, Book: book, Rates: rates, Contribution: contribution, ValidDays: validDays}
	var err error
	if rc.CandFile, err = filepath.Abs(candFile); err != nil {
		return err
	}
	if rc.CandHash, err = fileHash(rc.CandFile); err != nil {
		return err
	}
	if feeFile != "" {
		if rc.FeeFile, err = filepath.Abs(feeFile); err != nil {
			return err
		}
		if rc.FeeHash, err = fileHash(rc.FeeFile); err != nil {
			return err
		}
	}
	cr, err := correctionsRepo(a)
	if err != nil {
		return err
	}
	return cr.PutRecipe(ctx, rc)
}

type scanRow struct {
	Plan      string                `json:"plan"`
	Security  string                `json:"security"`
	Rev       int                   `json:"rev"`
	Outcome   string                `json:"outcome"`
	Change    string                `json:"change,omitempty"`
	NewRev    int                   `json:"new_rev,omitempty"`
	Findings  []corrections.Finding `json:"findings,omitempty"`
	Ignored   []string              `json:"ignored,omitempty"`
	Revoked   []string              `json:"revoked,omitempty"`
	Submitted []string              `json:"submitted,omitempty"`
}

func correctionsScan(ctx context.Context, a *app, args []string) error {
	fs := newFlags("corrections scan")
	asJSON := fs.Bool("json", false, "print as JSON")
	noAlert := fs.Bool("no-alert", false, "do not record the alert (alerts scan will)")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	cr, err := correctionsRepo(a)
	if err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	repo := plan.NewRepo(rs)
	var rows []scanRow

	// A change that was found and not finished (the program stopped) is finished
	// first, from what was written down when it was found.
	chs, err := cr.Changes(ctx)
	if err != nil {
		return err
	}
	for _, c := range chs {
		if c.State != corrections.Detected {
			continue
		}
		done, err := finishChange(ctx, a, cr, repo, c)
		if err != nil {
			return err
		}
		rows = append(rows, scanRow{Plan: c.Plan, Rev: c.FromRev, Outcome: "finished after an interruption", Change: c.ID, NewRev: done.ToRev, Findings: c.Findings, Revoked: done.Revoked, Submitted: done.Submitted})
	}

	plans, err := repo.Plans(ctx)
	if err != nil {
		return err
	}
	rules, err := cr.Rules(ctx, "")
	if err != nil {
		return err
	}
	for _, rv := range plans {
		row := scanRow{Plan: rv.PlanID, Security: rv.Card.Security, Rev: rv.Rev}
		st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return err
		}
		switch eff := plan.Effective(st, rv.Card.Expiry, a.now()); eff {
		case plan.Draft, plan.Proposed, plan.Accepted:
		default:
			row.Outcome = "not live (" + string(eff) + ")"
			rows = append(rows, row)
			continue
		}
		deps, ok, err := cr.Deps(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return err
		}
		if !ok {
			row.Outcome = "untracked: made before dependencies were kept; generate it again to track it"
			rows = append(rows, row)
			continue
		}
		found, ignored, err := detect(ctx, a, rv, deps, rules)
		if err != nil {
			return err
		}
		row.Ignored = ignored
		if len(found) == 0 {
			row.Outcome = "unchanged"
			rows = append(rows, row)
			continue
		}
		id := corrections.ChangeID(rv.PlanID, found)
		row.Change, row.Findings = id, found
		if old, ok, err := cr.Change(ctx, rv.PlanID, id); err != nil {
			return err
		} else if ok && old.State == corrections.Done {
			row.Outcome, row.NewRev = "already handled", old.ToRev
			rows = append(rows, row)
			continue
		}
		c := corrections.Change{Plan: rv.PlanID, ID: id, State: corrections.Detected, FromRev: rv.Rev, Book: rv.Card.Book,
			Day: a.now().UTC().Format(time.DateOnly), Strength: corrections.Strongest(found), Findings: found, At: a.now().UTC()}
		if err := cr.PutChange(ctx, c); err != nil {
			return err
		}
		done, err := finishChange(ctx, a, cr, repo, c)
		if err != nil {
			return err
		}
		row.Outcome, row.NewRev, row.Revoked, row.Submitted = "revised: "+string(c.Strength), done.ToRev, done.Revoked, done.Submitted
		rows = append(rows, row)
	}
	raised := 0
	if !*noAlert && changedAny(rows) {
		n, err := raiseAlerts(ctx, a)
		if err != nil {
			return err
		}
		raised = n
	}
	if *asJSON {
		return printJSON(a, rows)
	}
	writeScan(a, rows, raised)
	return nil
}

func changedAny(rows []scanRow) bool {
	for _, r := range rows {
		if r.NewRev > 0 && !strings.HasPrefix(r.Outcome, "already") {
			return true
		}
	}
	return false
}

// raiseAlerts records the alerts that hold now, without delivering them.
func raiseAlerts(ctx context.Context, a *app) (int, error) {
	ob, err := alertOutbox(a)
	if err != nil {
		return 0, err
	}
	cfg, _, err := ob.Config(ctx)
	if err != nil {
		return 0, err
	}
	src, err := workflowSource(a)
	if err != nil {
		return 0, err
	}
	sc, errs := src.AlertScan(ctx, cfg)
	res, err := ob.Raise(ctx, cfg, sc)
	if err != nil {
		return 0, err
	}
	for _, e := range errs {
		fmt.Fprintf(a.stderr, "stoxicity: not looked at: %v\n", e)
	}
	return len(res.New), nil
}

func writeScan(a *app, rows []scanRow, raised int) {
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, "no plans to look at (plan generate makes them)")
		return
	}
	color := useColor(a.stdout)
	t := cliTable{Header: []string{"plan", "rev", "security", "result", "new rev", "change"}, Right: []bool{false, true, false, false, true, false}}
	for _, r := range rows {
		st := cliPlain
		switch {
		case strings.HasPrefix(r.Outcome, "revised"), strings.HasPrefix(r.Outcome, "finished"):
			st = cliYellow
		case r.Outcome == "unchanged":
			st = cliGreen
		}
		nr := ""
		if r.NewRev > 0 {
			nr = fmt.Sprint(r.NewRev)
		}
		t.Rows = append(t.Rows, []cliCell{plain(r.Plan), plain(fmt.Sprint(r.Rev)), plain(r.Security), styled(r.Outcome, st), plain(nr), plain(r.Change)})
	}
	t.write(a.stdout, color)
	for _, r := range rows {
		for _, f := range r.Findings {
			fmt.Fprintf(a.stdout, "\n%s: %s (%s)", r.Plan, f.Detail, f.Effect)
		}
		for _, g := range r.Ignored {
			fmt.Fprintf(a.stdout, "\n%s: not material: %s", r.Plan, g)
		}
		if len(r.Revoked) > 0 {
			fmt.Fprintf(a.stdout, "\n%s: confirmation withdrawn from %s; confirm again after you accept the new revision", r.Plan, strings.Join(r.Revoked, ", "))
		}
		if len(r.Submitted) > 0 {
			fmt.Fprintf(a.stdout, "\n%s: already sent and left exactly as it is: %s (order show ID)", r.Plan, strings.Join(r.Submitted, ", "))
		}
	}
	fmt.Fprintf(a.stdout, "\n\n%d new alert(s) recorded. A changed plan is a new draft: propose and accept it again, then confirm any order again. Nothing was placed, changed or cancelled at a broker.\n", raised)
}

// detect lists the material changes of a revision.
func detect(ctx context.Context, a *app, rv plan.Revision, d corrections.Deps, rules []corrections.Rule) ([]corrections.Finding, []string, error) {
	db, err := a.store()
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(d.Events))
	for i, p := range d.Events {
		ids[i] = p.EventID
	}
	cur, err := corrections.Observe(ctx, db, ids)
	if err != nil {
		return nil, nil, err
	}
	found, ignored := corrections.Compare(d.Events, cur)

	// thesis rules: a match that is new since the revision was made
	var mine []corrections.Rule
	for _, r := range rules {
		if r.Security == rv.Card.Security {
			mine = append(mine, r)
		}
	}
	if len(mine) > 0 {
		all, _, _, err := evidenceOf(ctx, a, rv.Card.Security)
		if err != nil {
			return nil, nil, err
		}
		found = append(found, corrections.NewRuleHits(mine, all, d)...)
	}

	// figures and recorded decisions the revision was computed from
	curMan, err := currentManifest(ctx, a, rv)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range rv.Manifest.Changed(curMan) {
		switch name {
		case "scorecard":
			found = append(found, corrections.Finding{Kind: "figure_revised", Ref: "scorecard", Effect: corrections.Revise, Detail: "a figure or note the plan's scorecard rests on was recorded or revised after the plan was made", To: curMan.Scorecard})
		case "valuation assumptions":
			found = append(found, corrections.Finding{Kind: "figure_revised", Ref: "valuation assumptions", Effect: corrections.Revise, Detail: "the valuation assumptions the plan used were changed", To: curMan.Assumptions})
		case "recorded decisions":
			found = append(found, corrections.Finding{Kind: "figure_revised", Ref: "recorded decisions", Effect: corrections.Review, Detail: "your recorded thesis, instruction or review for this holding changed after the plan was made", To: curMan.Decisions})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Kind+found[i].Ref < found[j].Kind+found[j].Ref })
	return found, ignored, nil
}

// finishChange does what a found change needs, in an order that can be repeated:
// the revision (found again by the change it carries), what it depends on, the
// withdrawal of confirmations, and the record that it is done.
func finishChange(ctx context.Context, a *app, cr corrections.Repo, repo plan.Repo, c corrections.Change) (corrections.Change, error) {
	revs, err := repo.Revisions(ctx, c.Plan)
	if err != nil {
		return c, err
	}
	var made *plan.Revision
	var from plan.Revision
	for i := range revs {
		if revs[i].Rev == c.FromRev {
			from = revs[i]
		}
		if revs[i].Rev > c.FromRev {
			for _, l := range revs[i].Card.Corrections {
				if strings.HasPrefix(l, "change "+c.ID+":") {
					made = &revs[i]
				}
			}
		}
	}
	if from.PlanID == "" {
		return c, fmt.Errorf("plan %s revision %d is gone", c.Plan, c.FromRev)
	}
	if made == nil {
		card, man, err := reassess(ctx, a, cr, from, c)
		if err != nil {
			return c, err
		}
		rv, err := plan.New(card, man, a.now())
		if err != nil {
			return c, fmt.Errorf("the corrected plan is not valid: %w", err)
		}
		sv, err := repo.Save(ctx, rv, a.now())
		if err != nil {
			return c, err
		}
		made = &sv.Revision
	}
	if err := trackRevision(ctx, a, *made); err != nil {
		return c, err
	}
	c.ToRev = made.Rev
	// Which confirmations will be withdrawn is written down before any is, so a
	// restart that finds them withdrawn already still reports them.
	pending, sent, err := openOrdersOf(ctx, a, c)
	if err != nil {
		return c, err
	}
	c.Revoked, c.Submitted = union(c.Revoked, pending), union(c.Submitted, sent)
	if err := cr.PutChange(ctx, c); err != nil {
		return c, err
	}
	if err := withdrawConfirmations(ctx, a, c); err != nil {
		return c, err
	}
	c.State = corrections.Done
	if err := cr.PutChange(ctx, c); err != nil {
		return c, err
	}
	return c, nil
}

// reassess builds the card of the revision a change calls for. When the plan can
// be generated again from its recipe and the change is a clear one, it is; in
// every other case the earlier card is kept, proposes nothing, and says what
// must be looked at: a revision that cannot be reassessed is never a fresh HOLD.
func reassess(ctx context.Context, a *app, cr corrections.Repo, from plan.Revision, c corrections.Change) (plan.Card, plan.Manifest, error) {
	explain := []string{fmt.Sprintf("change %s: %d relevant change(s) since revision %d; the earlier revision and your decision about it are kept", c.ID, len(c.Findings), from.Rev)}
	for _, f := range c.Findings {
		explain = append(explain, f.Detail)
	}
	var reasons []string
	var card plan.Card
	var man plan.Manifest
	regenerated := false
	switch {
	case c.Strength != corrections.Revise:
		reasons = append(reasons, "a change needs a person's judgement: "+string(c.Strength))
	default:
		rc, ok, err := cr.Recipe(ctx, c.Plan)
		if err != nil {
			return card, man, err
		}
		if !ok {
			reasons = append(reasons, "this plan has no recipe to generate it again")
			break
		}
		var note string
		card, man, note, err = regenerate(ctx, a, rc, from, c.Day)
		if err != nil {
			reasons = append(reasons, "it could not be generated again: "+err.Error())
			break
		}
		if note != "" {
			explain = append(explain, note)
		}
		regenerated = true
	}
	if !regenerated {
		card, man = from.Card, from.Manifest
		card.AsOf = c.Day
		if e, err := time.Parse(time.DateOnly, card.Expiry); err != nil || e.Before(mustDay(c.Day)) {
			card.Expiry = mustDay(c.Day).AddDate(0, 0, 3).Format(time.DateOnly)
		}
		card.Action, card.Sizing, card.Concentration = "ABSTAIN", nil, nil
		card.ExecutionEligibility, card.StrategyState = "research_only", "review required"
		card.Review = append(reasons, "read the changes above before this plan is used again")
		card.Reasons = append([]string{"not reassessed: the earlier revision is shown unchanged and proposes nothing"}, card.Reasons...)
		card.Unknowns = append(card.Unknowns, "the plan was not reassessed after the change above")
	}
	_, sup, _, err := evidenceOf(ctx, a, card.Security)
	if err != nil {
		return card, man, err
	}
	card.Evidence, man.Evidence = corrections.Lines(sup), corrections.Hash(sup)
	card.Corrections = explain
	return card, man, nil
}

func mustDay(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

// regenerate runs the plan again from its recipe and returns the card of the
// same plan.
func regenerate(ctx context.Context, a *app, rc corrections.Recipe, from plan.Revision, day string) (plan.Card, plan.Manifest, string, error) {
	var note string
	if h, err := fileHash(rc.CandFile); err != nil {
		return plan.Card{}, plan.Manifest{}, "", fmt.Errorf("the candidate file %s cannot be read", filepath.Base(rc.CandFile))
	} else if h != rc.CandHash {
		note = "the candidate file changed since the plan was made; its current content was used"
	}
	if rc.FeeFile != "" {
		if h, err := fileHash(rc.FeeFile); err != nil {
			return plan.Card{}, plan.Manifest{}, "", fmt.Errorf("the fee file %s cannot be read", filepath.Base(rc.FeeFile))
		} else if h != rc.FeeHash {
			note = strings.TrimSpace(note + "; the fee file changed since the plan was made; its current content was used")
		}
	}
	gt, err := gatherAllocation(ctx, a, allocParams{Book: rc.Book, CandFile: rc.CandFile, FeeFile: rc.FeeFile, Rates: rc.Rates, Contribution: rc.Contribution, Day: day})
	if err != nil {
		return plan.Card{}, plan.Manifest{}, "", err
	}
	gt.Plan = allocate.Allocate(gt.Req)
	expiry := mustDay(day).AddDate(0, 0, max(rc.ValidDays, 1)).Format(time.DateOnly)
	cards, mans, err := buildCards(gt, day, expiry)
	if err != nil {
		return plan.Card{}, plan.Manifest{}, "", err
	}
	for i, c := range cards {
		if plan.PlanID(c.Book, c.Security, c.Sleeve, c.Strategy) == from.PlanID {
			return c, mans[i], note, nil
		}
	}
	return plan.Card{}, plan.Manifest{}, "", errors.New("the plan is no longer produced by its recipe (the security, sleeve or strategy changed)")
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string{}, a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// openOrdersOf lists the orders of the changed plan: those confirmed and not
// sent, whose confirmation is withdrawn, and those already sent, which are only
// listed.
func openOrdersOf(ctx context.Context, a *app, c corrections.Change) (unsent, sent []string, err error) {
	ps, err := a.portfolios()
	if err != nil {
		return nil, nil, err
	}
	b, err := ps.FindBook(ctx, c.Book)
	if err != nil {
		return nil, nil, err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	vs, err := svc.Views(ctx, b.Account)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range vs {
		if v.Terms.PlanID != c.Plan || v.Terms.Book != b.ID {
			continue
		}
		switch {
		case v.Sent():
			sent = append(sent, v.ID)
		case v.State == order.Confirmed:
			unsent = append(unsent, v.ID)
		}
	}
	sort.Strings(unsent)
	sort.Strings(sent)
	return unsent, sent, nil
}

// withdrawConfirmations withdraws the confirmation of every order of the plan
// that was confirmed and not sent. An order that was sent is not touched.
func withdrawConfirmations(ctx context.Context, a *app, c corrections.Change) error {
	ps, err := a.portfolios()
	if err != nil {
		return err
	}
	b, err := ps.FindBook(ctx, c.Book)
	if err != nil {
		return err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return err
	}
	reason := fmt.Sprintf("the evidence behind plan %s changed (%s): confirm again after reviewing the new revision", c.Plan, c.ID)
	_, err = svc.RevokeWhere(ctx, b.ID, func(v order.View) bool { return v.Terms.PlanID == c.Plan }, reason)
	return err
}

func correctionsList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("corrections list")
	asJSON := fs.Bool("json", false, "print as JSON")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	cr, err := correctionsRepo(a)
	if err != nil {
		return err
	}
	chs, err := cr.Changes(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		if chs == nil {
			chs = []corrections.Change{}
		}
		return printJSON(a, chs)
	}
	if len(chs) == 0 {
		fmt.Fprintln(a.stdout, "no corrections were found (corrections scan looks)")
		return nil
	}
	t := cliTable{Header: []string{"change", "plan", "from", "to", "state", "strength", "found"}, Right: []bool{false, false, true, true, false, false, false}}
	for _, c := range chs {
		st := cliGreen
		if c.State != corrections.Done {
			st = cliYellow
		}
		t.Rows = append(t.Rows, []cliCell{plain(c.ID), plain(c.Plan), plain(fmt.Sprint(c.FromRev)), plain(fmt.Sprint(c.ToRev)), styled(c.State, st), plain(string(c.Strength)), plain(c.At.UTC().Format("2006-01-02 15:04"))})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func correctionsShow(ctx context.Context, a *app, args []string) error {
	if len(args) != 1 {
		return usageErrorf("usage: corrections show CHANGE")
	}
	cr, err := correctionsRepo(a)
	if err != nil {
		return err
	}
	chs, err := cr.Changes(ctx)
	if err != nil {
		return err
	}
	for _, c := range chs {
		if c.ID != args[0] {
			continue
		}
		fmt.Fprintf(a.stdout, "%s on plan %s: revision %d -> %d (%s, %s)\n", c.ID, c.Plan, c.FromRev, c.ToRev, c.State, c.Strength)
		for _, f := range c.Findings {
			fmt.Fprintf(a.stdout, "  - %s: %s\n", f.Effect, f.Detail)
		}
		if len(c.Revoked) > 0 {
			fmt.Fprintf(a.stdout, "confirmation withdrawn: %s\n", strings.Join(c.Revoked, ", "))
		}
		if len(c.Submitted) > 0 {
			fmt.Fprintf(a.stdout, "already sent, left unchanged: %s\n", strings.Join(c.Submitted, ", "))
		}
		return nil
	}
	return fmt.Errorf("no correction %s (corrections list shows them)", args[0])
}

func correctionsRule(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: add | list | retire")
	}
	cr, err := correctionsRepo(a)
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		fs := newFlags("corrections rule add")
		sec := fs.String("security", "", "security ID")
		id := fs.String("id", "", "rule ID, e.g. dividend-cut")
		kinds := fs.String("kinds", "", "comma-separated event kinds that break the thesis, e.g. bankruptcy,non_reliance")
		roles := fs.String("roles", "", "roles the security must have (default subject): subject,counterparty,mentioned")
		effect := fs.String("effect", "review", "broken (the thesis is broken) or review (a person must look)")
		note := fs.String("note", "", "why the thesis fails when this matches")
		by := fs.String("by", "", "who records the rule")
		if rest, err := parseFlags(fs, args[1:]); err != nil {
			return err
		} else if len(rest) > 0 {
			return usageErrorf("unexpected argument %q", rest[0])
		}
		r := corrections.Rule{ID: *id, Security: *sec, Effect: corrections.Effect(*effect), Note: *note, By: *by}
		for _, k := range strings.Split(*kinds, ",") {
			if k = strings.TrimSpace(k); k != "" {
				r.Kinds = append(r.Kinds, k)
			}
		}
		for _, k := range strings.Split(*roles, ",") {
			if k = strings.TrimSpace(k); k != "" {
				r.Roles = append(r.Roles, k)
			}
		}
		got, err := cr.PutRule(ctx, r, a.now())
		if err != nil {
			return usageErrorf("%v", err)
		}
		fmt.Fprintf(a.stdout, "thesis rule %s for %s recorded as version %d. A plan is told only when a matching event appears after the plan was made; a rule never sells anything.\n", got.ID, got.Security, got.Version)
		return nil
	case "list":
		rs, err := cr.Rules(ctx, "")
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			fmt.Fprintln(a.stdout, "no thesis rules are recorded (corrections rule add)")
			return nil
		}
		t := cliTable{Header: []string{"security", "rule", "version", "effect", "kinds", "roles", "state", "note"}, Right: []bool{false, false, true}}
		for _, r := range rs {
			state := "in force"
			if r.Retired {
				state = "retired"
			}
			roles := strings.Join(r.Roles, ",")
			if roles == "" {
				roles = "subject"
			}
			t.Rows = append(t.Rows, []cliCell{plain(r.Security), plain(r.ID), plain(fmt.Sprint(r.Version)), plain(string(r.Effect)), plain(strings.Join(r.Kinds, ",")), plain(roles), plain(state), plain(r.Note)})
		}
		t.write(a.stdout, useColor(a.stdout))
		return nil
	case "retire":
		fs := newFlags("corrections rule retire")
		sec := fs.String("security", "", "security ID")
		id := fs.String("id", "", "rule ID")
		by := fs.String("by", "", "who retires it")
		if rest, err := parseFlags(fs, args[1:]); err != nil {
			return err
		} else if len(rest) > 0 {
			return usageErrorf("unexpected argument %q", rest[0])
		}
		if *sec == "" || *id == "" || *by == "" {
			return usageErrorf("usage: corrections rule retire -security S -id R -by NAME")
		}
		r, err := cr.Retire(ctx, *sec, *id, *by, a.now())
		if err != nil {
			return usageErrorf("%v", err)
		}
		fmt.Fprintf(a.stdout, "thesis rule %s retired at version %d; its history stays.\n", r.ID, r.Version)
		return nil
	}
	return usageErrorf("unknown subcommand %q (add, list, retire)", args[0])
}
