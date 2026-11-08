package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/alerts"
	"github.com/b070nd/stoxicity/app/src/journal"
	"github.com/b070nd/stoxicity/app/src/plan"
)

// cmdJournal keeps the reasoning behind decisions and looks back at them. An
// entry copies the plan's thesis as it stood when the user decided; an outcome
// is linked to it later. Nothing already recorded is rewritten.
func cmdJournal(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: add | outcome | show | list")
	}
	switch args[0] {
	case "add":
		return journalAdd(ctx, a, args[1:])
	case "outcome":
		return journalOutcome(ctx, a, args[1:])
	case "show":
		return journalShow(ctx, a, args[1:])
	case "list":
		return journalList(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (add, outcome, show, list)", args[0])
}

func journalRepo(a *app) (journal.Repo, error) {
	rs, err := a.records()
	if err != nil {
		return journal.Repo{}, err
	}
	return journal.Repo{RS: rs, Now: a.now}, nil
}

func journalAdd(ctx context.Context, a *app, args []string) error {
	fs := newFlags("journal add")
	decision := fs.String("decision", "", "accepted, rejected, deferred or noted")
	reason := fs.String("reason", "", "why, in your own words (required)")
	by := fs.String("by", "", "who is deciding")
	horizon := fs.String("horizon", "", "the day to look back, YYYY-MM-DD (default: the plan's review date)")
	alertRef := fs.String("alert", "", "the alert this answers")
	apply := fs.Bool("apply", false, "also move the plan to accepted or rejected")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *decision == "" {
		return usageErrorf("usage: journal add PLAN[.rN] -decision accepted|rejected|deferred|noted -reason TEXT -by NAME [-horizon DAY] [-alert ID] [-apply]")
	}
	id, n, err := planRef(rest[0])
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
	day := a.now().UTC().Format(time.DateOnly)
	var ob alerts.Outbox
	var al alerts.Alert
	alertKey := ""
	if *alertRef != "" {
		if ob, err = alertOutbox(a); err != nil {
			return err
		}
		if al, err = ob.Find(ctx, *alertRef); err != nil {
			return err
		}
		alertKey = al.Key()
	}
	e, err := journal.New(rv, journal.Decision(*decision), *reason, *by, *horizon, alertKey, day)
	if err != nil {
		return usageErrorf("%v", err)
	}
	// The journal never claims a decision the plan does not have.
	if e.Decision == journal.Accepted || e.Decision == journal.Rejected {
		want := plan.State(e.Decision)
		st, _, err := repo.State(ctx, rv.PlanID, rv.Rev)
		if err != nil {
			return err
		}
		switch {
		case st == want:
		case *apply:
			if err := planMove(ctx, a, want, []string{rv.ID(), "-by", *by, "-note", *reason}); err != nil {
				return err
			}
		default:
			return usageErrorf("%s is %s, not %s. Add -apply to move the plan as well, or decide it first with plan accept|reject; the journal does not record a decision the plan does not have", rv.ID(), st, want)
		}
	} else if *apply {
		return usageErrorf("-apply only moves a plan to accepted or rejected")
	}
	jr, err := journalRepo(a)
	if err != nil {
		return err
	}
	created, err := jr.Add(ctx, e)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(a.stdout, "%s recorded: %s %s (%s), look back on %s.\n", e.ID, e.Decision, rv.ID(), e.Security, e.Horizon)
	} else {
		fmt.Fprintf(a.stdout, "%s is already recorded; nothing changed.\n", e.ID)
	}
	if *alertRef != "" {
		kind := "noted"
		switch e.Decision {
		case journal.Accepted:
			kind = "accepted"
		case journal.Rejected:
			kind = "rejected"
		}
		if _, err := ob.Respond(ctx, al.Key(), kind, "journal "+e.ID+": "+e.Reason, *by); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "alert %s answered %s.\n", al.ID(), kind)
	}
	return nil
}

func journalOutcome(ctx context.Context, a *app, args []string) error {
	fs := newFlags("journal outcome")
	result := fs.String("result", "", "held, broken, mixed or early (too soon to say)")
	note := fs.String("note", "", "what happened, in your own words (required)")
	by := fs.String("by", "", "who is looking back")
	day := fs.String("day", "", "the day of the look (default today)")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *result == "" {
		return usageErrorf("usage: journal outcome ENTRY -result held|broken|mixed|early -note TEXT -by NAME [-day DAY]")
	}
	d := *day
	if d == "" {
		d = a.now().UTC().Format(time.DateOnly)
	}
	jr, err := journalRepo(a)
	if err != nil {
		return err
	}
	o, err := jr.AddOutcome(ctx, rest[0], journal.Result(*result), *note, *by, d)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if o.Finalizes() {
		fmt.Fprintf(a.stdout, "outcome of %s recorded: thesis %s. The entry and the plan are unchanged.\n", o.Entry, o.Result)
	} else {
		fmt.Fprintf(a.stdout, "outcome of %s recorded: too early to say. The entry stays due.\n", o.Entry)
	}
	return nil
}

func journalShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("journal show")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("usage: journal show ENTRY [-json]")
	}
	jr, err := journalRepo(a)
	if err != nil {
		return err
	}
	e, err := jr.Get(ctx, rest[0])
	if err != nil {
		return err
	}
	os, err := jr.Outcomes(ctx, e.ID)
	if err != nil {
		return err
	}
	// the plan as it is now, to read the thesis then against the thesis now
	repo, err := planRepo(a)
	if err != nil {
		return err
	}
	var drift []journal.Drift
	nowRev := ""
	moved := false
	if latest, err := repo.Latest(ctx, e.PlanID); err == nil {
		drift = journal.Compare(e, latest.Card)
		nowRev = latest.ID()
		moved = latest.ManifestHash != e.Manifest
	}
	if os == nil {
		os = []journal.Outcome{}
	}
	if *asJSON {
		return printJSON(a, map[string]any{"entry": e, "outcomes": os, "plan_now": nowRev, "plan_inputs_changed": moved, "thesis_drift": drift})
	}
	fmt.Fprintf(a.stdout, "%s  %s %s on %s by %s\n  plan %s (%s %s), manifest %s\n  look back on %s\n  reason: %s\n", e.ID, e.Decision, e.Security, e.Day, e.By, e.Plan, e.Action, e.Security, e.Manifest, e.Horizon, e.Reason)
	if e.Alert != "" {
		fmt.Fprintf(a.stdout, "  answers alert: %s\n", e.Alert)
	}
	list := func(title string, xs []string) {
		fmt.Fprintf(a.stdout, "\n%s when decided:\n", title)
		if len(xs) == 0 {
			fmt.Fprintln(a.stdout, "  (the plan stated none)")
		}
		for _, x := range xs {
			fmt.Fprintf(a.stdout, "  - %s\n", x)
		}
	}
	list("Thesis", e.Thesis)
	list("Invalidation", e.Invalidation)
	list("Exit rules", e.ExitRules)
	list("Counter-evidence", e.Counter)
	if nowRev != "" {
		fmt.Fprintf(a.stdout, "\nThe plan now is %s", nowRev)
		if nowRev == e.Plan {
			fmt.Fprint(a.stdout, " (the same revision)")
		}
		if moved {
			fmt.Fprint(a.stdout, "; its inputs differ from those you decided on")
		}
		fmt.Fprintln(a.stdout, ".")
		any := false
		for _, d := range drift {
			for _, g := range d.Gone {
				fmt.Fprintf(a.stdout, "  %s no longer stated: %s\n", d.Section, g)
				any = true
			}
			for _, g := range d.New {
				fmt.Fprintf(a.stdout, "  %s now also states: %s\n", d.Section, g)
				any = true
			}
		}
		if !any {
			fmt.Fprintln(a.stdout, "  the thesis, invalidation, exit rules and counter-evidence read as they did.")
		}
	}
	fmt.Fprintln(a.stdout, "\nLooks back:")
	if len(os) == 0 {
		fmt.Fprintln(a.stdout, "  none yet")
	}
	for _, o := range os {
		fmt.Fprintf(a.stdout, "  %s by %s: thesis %s - %s\n", o.Day, o.By, o.Result, o.Note)
	}
	return nil
}

func journalList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("journal list")
	due := fs.Bool("due", false, "only entries whose horizon has come and that no look back has closed")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	jr, err := journalRepo(a)
	if err != nil {
		return err
	}
	var es []journal.Entry
	if *due {
		es, err = jr.Due(ctx, a.now().UTC().Format(time.DateOnly))
	} else {
		es, err = jr.List(ctx)
	}
	if err != nil {
		return err
	}
	if es == nil {
		es = []journal.Entry{}
	}
	if *asJSON {
		return printJSON(a, es)
	}
	if len(es) == 0 {
		fmt.Fprintln(a.stdout, "no entries")
		return nil
	}
	t := cliTable{Header: []string{"entry", "day", "decision", "plan", "security", "horizon", "looked back", "reason"}}
	for _, e := range es {
		os, err := jr.Outcomes(ctx, e.ID)
		if err != nil {
			return err
		}
		lb, st := "no", cliYellow
		if n := len(os); n > 0 {
			lb, st = string(os[n-1].Result), cliPlain
			if !journal.Closed(os) {
				st = cliYellow
			}
		}
		t.Rows = append(t.Rows, []cliCell{plain(e.ID), plain(e.Day), plain(string(e.Decision)), plain(e.Plan), plain(e.Security), plain(e.Horizon), styled(lb, st), plain(strings.TrimSpace(e.Reason))})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}
