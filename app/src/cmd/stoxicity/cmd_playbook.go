package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/research"
)

// labGate adapts the tier gate to the risk layer of the playbook. It approves
// the drafting of an order of a kind; it never places, drafts or confirms one.
type labGate struct {
	gate *order.Gate
	err  error
}

// Approve implements playbook.Gate.
func (g labGate) Approve(s playbook.Signal) playbook.Decision {
	if g.err != nil || g.gate == nil {
		reason := "the tier gate is not available"
		if g.err != nil {
			reason = g.err.Error()
		}
		return playbook.Decision{By: "tier gate", Reasons: []string{reason}}
	}
	by := fmt.Sprintf("tier gate (tier %d)", g.gate.Tier)
	switch s.Action {
	case playbook.Buy:
		if len(g.gate.Suspended) > 0 {
			return playbook.Decision{By: by, Reasons: append([]string(nil), g.gate.Suspended...)}
		}
	case playbook.Sell:
	default:
		return playbook.Decision{By: by, Reasons: []string{"only a buy or a sell is offered for approval"}}
	}
	mode := "real"
	if g.gate.Tier < 1 {
		mode = "paper"
	}
	return playbook.Decision{Approved: true, Mode: mode, By: by,
		Reasons: []string{"this approves drafting an order of that kind; size and limits are checked by the policy when the order is drafted"}}
}

// cmdPlaybook manages versioned signal rules and reads them against a study.
func cmdPlaybook(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | check | register | list | show | signals | history")
	}
	switch args[0] {
	case "example":
		if len(args) > 1 {
			return usageErrorf("playbook example takes no arguments")
		}
		return labPlaybookExample(a)
	case "check":
		return labPlaybookFile(ctx, a, args[1:], false)
	case "register":
		return labPlaybookFile(ctx, a, args[1:], true)
	case "list":
		return labPlaybookList(ctx, a, args[1:])
	case "show":
		return labPlaybookShow(ctx, a, args[1:])
	case "signals":
		return labPlaybookSignals(ctx, a, args[1:])
	case "history":
		return labPlaybookHistory(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (example, check, register, list, show, signals, history)", args[0])
}

func labPlaybookExample(a *app) error {
	fmt.Fprintln(a.stdout, "# playbook.json: a rule may only test what its study also selected on.")
	fmt.Fprintln(a.stdout, "# buy or sell is only given when the study has an evaluated validation group; otherwise the answer is watch.")
	fmt.Fprintln(a.stdout, "# trade (a buy only) is what `walkfwd from-playbook` replays with fills: the amount, and how far the limit and the stop lie from the last close.")
	fmt.Fprintln(a.stdout, "# Its numbers are arbitrary starting points to review, not findings; they are part of the rule and of its hash.")
	return writeJSON(a.stdout, playbook.Playbook{
		ID: "insider-buys-pb-v1", Study: "insider-buys-v1",
		Rules: []playbook.Rule{{
			ID: "buy-insider-buys", Types: []string{"insider_buy"}, Action: playbook.Buy,
			Evidence: playbook.Evidence{Sessions: 20, MinEvents: 50, Direction: "up", MinEdge: 0.01},
			Trade:    &playbook.Trade{Notional: "1000", LimitOffset: 0.005, InvalidationOffset: 0.08},
		}},
	})
}

func labPlaybookFile(ctx context.Context, a *app, args []string, register bool) error {
	name := "playbook check"
	if register {
		name = "playbook register"
	}
	fs := newFlags(name)
	file := fs.String("file", "", "playbook JSON (playbook example shows one)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: %s -file F", name)
	}
	var p playbook.Playbook
	if err := readJSONFile(*file, &p); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if !register {
		fmt.Fprintf(a.stdout, "playbook %s is valid (hash %s).\n", p.ID, p.Hash()[:12])
		return nil
	}
	lab, err := a.lab()
	if err != nil {
		return err
	}
	reg, err := lab.RegisterPlaybook(ctx, p)
	if err != nil {
		return err
	}
	switch {
	case reg.Unchanged:
		fmt.Fprintf(a.stdout, "playbook %s is already registered with this content (revision %d, hash %s).\n", p.ID, reg.Revision, p.Hash()[:12])
	case reg.Existed:
		fmt.Fprintf(a.stdout, "playbook %s registered as revision %d, a new version of the rules (hash %s).\n", p.ID, reg.Revision, p.Hash()[:12])
	default:
		fmt.Fprintf(a.stdout, "playbook %s registered as revision %d (hash %s).\n", p.ID, reg.Revision, p.Hash()[:12])
	}
	known, err := lab.HasStudy(ctx, p.Study)
	if err != nil {
		return err
	}
	if !known {
		fmt.Fprintf(a.stdout, "study %s is not registered yet.\n", p.Study)
	}
	return nil
}

func labPlaybookList(ctx context.Context, a *app, args []string) error {
	rest, err := parseFlags(newFlags("playbook list"), args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("playbook list takes no arguments, got %q", rest[0])
	}
	lab, err := a.lab()
	if err != nil {
		return err
	}
	pbs, err := lab.Playbooks(ctx)
	if err != nil {
		return err
	}
	if len(pbs) == 0 {
		fmt.Fprintln(a.stdout, "no playbook is registered (playbook example, playbook register).")
		return nil
	}
	t := cliTable{Header: []string{"playbook", "study", "rules", "revisions", "hash"}, Right: []bool{2: true, 3: true}}
	for _, s := range pbs {
		p := s.Playbook
		t.Rows = append(t.Rows, []cliCell{styled(p.ID, cliStrong), plain(p.Study), plain(fmt.Sprint(len(p.Rules))), plain(fmt.Sprint(s.Revisions)), plain(p.Hash()[:12])})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func labPlaybookShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("playbook show")
	asJSON := fs.Bool("json", false, "print the JSON alone")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: playbook show ID [-json]")
	}
	lab, err := a.lab()
	if err != nil {
		return err
	}
	p, rev, err := lab.LatestPlaybook(ctx, pos[0])
	if err != nil {
		return err
	}
	if !*asJSON {
		fmt.Fprintf(a.stdout, "# playbook %s, revision %d, hash %s\n", p.ID, rev, p.Hash()[:12])
	}
	return writeJSON(a.stdout, p)
}

func labPlaybookSignals(ctx context.Context, a *app, args []string) error {
	fs := newFlags("playbook signals")
	days := fs.Int("days", 7, "events that became available in the last N days")
	from := fs.String("from", "", "first day, YYYY-MM-DD (with -to)")
	to := fs.String("to", "", "last day, YYYY-MM-DD, inclusive (with -from)")
	save := fs.Bool("save", false, "keep each signal with the decision of the risk layer")
	asJSON := fs.Bool("json", false, "print as JSON")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: playbook signals ID [-days 7 | -from DAY -to DAY] [-save] [-json]")
	}
	start, end, err := signalWindow(a, fs, *days, *from, *to)
	if err != nil {
		return err
	}
	lab, err := a.lab()
	if err != nil {
		return err
	}
	_, _, signals, err := playbookSignals(ctx, a, lab, pos[0], start, end)
	if err != nil {
		return err
	}
	g, gerr := tierGate(ctx, a)
	gate := labGate{gate: g, err: gerr}
	decisions := make(map[string]playbook.Decision, len(signals))
	for _, s := range signals {
		decisions[s.ID] = playbook.Approve(gate, s)
	}
	fresh := 0
	if *save {
		kept := make([]research.SavedSignal, 0, len(signals))
		for _, s := range signals {
			kept = append(kept, research.SavedSignal{Signal: s, Decision: decisions[s.ID]})
		}
		if fresh, err = lab.SaveSignals(ctx, kept); err != nil {
			return err
		}
	}
	if *asJSON {
		out := map[string]any{"signals": signals, "decisions": decisions}
		if *save {
			out["saved_new"] = fresh
		}
		return writeJSON(a.stdout, out)
	}
	printSignals(a, signals, decisions)
	if *save {
		fmt.Fprintf(a.stdout, "saved %d new signal(s); %d were saved before.\n", fresh, len(signals)-fresh)
	}
	return nil
}

// signalWindow reads the -days, -from and -to flags of a command that computes
// signals: the last N days up to now, or the days from -from to -to inclusive.
// It takes the flag set, to tell whether -days was given, and the three values
// after parsing.
func signalWindow(a *app, fs *flag.FlagSet, days int, from, to string) (start, end time.Time, err error) {
	daysSet := false
	fs.Visit(func(f *flag.Flag) { daysSet = daysSet || f.Name == "days" })
	now := a.now()
	start, end = now.AddDate(0, 0, -days), now
	switch {
	case from != "" || to != "":
		if from == "" || to == "" || daysSet {
			return start, end, usageErrorf("give either -days or both -from and -to")
		}
		if start, err = parseDay(from, a.location()); err != nil {
			return start, end, usageErrorf("-from: %v", err)
		}
		last, err := parseDay(to, a.location())
		if err != nil {
			return start, end, usageErrorf("-to: %v", err)
		}
		if end = labEndOfDay(last); end.Before(start) {
			return start, end, usageErrorf("-to is before -from")
		}
	case days < 1:
		return start, end, usageErrorf("-days must be at least 1")
	}
	return start, end, nil
}

// playbookSignals evaluates a registered playbook on the events that became
// available in the window, against the newest run of the study it reads. It
// returns the playbook, the report the signals read, and the signals.
func playbookSignals(ctx context.Context, a *app, lab research.Lab, id string, start, end time.Time) (playbook.Playbook, eventstudy.Report, []playbook.Signal, error) {
	p, _, err := lab.LatestPlaybook(ctx, id)
	if err != nil {
		return p, eventstudy.Report{}, nil, err
	}
	runs, err := lab.Runs(ctx, p.Study)
	if err != nil {
		return p, eventstudy.Report{}, nil, err
	}
	if len(runs) == 0 {
		return p, eventstudy.Report{}, nil, fmt.Errorf("study %s has not been run: run `stoxicity study run %s` first", p.Study, p.Study)
	}
	report := runs[len(runs)-1].Report
	db, err := a.store()
	if err != nil {
		return p, report, nil, err
	}
	signals, err := research.Signals(ctx, db, research.SignalInput{Playbook: p, Report: report, From: start, To: end, Sectors: a.sectorMap()})
	return p, report, signals, err
}

func labPlaybookHistory(ctx context.Context, a *app, args []string) error {
	fs := newFlags("playbook history")
	asJSON := fs.Bool("json", false, "print as JSON")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("usage: playbook history [ID] [-json]")
	}
	lab, err := a.lab()
	if err != nil {
		return err
	}
	playbookID := ""
	if len(pos) == 1 {
		playbookID = pos[0]
	}
	saved, err := lab.SavedSignals(ctx, playbookID)
	if err != nil {
		return err
	}
	signals := make([]playbook.Signal, 0, len(saved))
	decisions := make(map[string]playbook.Decision, len(saved))
	for _, x := range saved {
		signals = append(signals, x.Signal)
		decisions[x.Signal.ID] = x.Decision
	}
	if *asJSON {
		return writeJSON(a.stdout, saved)
	}
	if len(signals) == 0 {
		fmt.Fprintln(a.stdout, "no signal has been saved (playbook signals ID -save).")
		return nil
	}
	printSignals(a, signals, decisions)
	return nil
}
