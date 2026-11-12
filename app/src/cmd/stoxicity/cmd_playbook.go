package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"sort"

	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/research"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	recLabPlaybook = "playbook.def"
	recLabSignal   = "playbook.signal"
)

// labSaved is a signal as kept, with the answer of the risk layer.
type labSaved struct {
	Signal   playbook.Signal   `json:"signal"`
	Decision playbook.Decision `json:"decision"`
}

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
	return writeJSON(a.stdout, playbook.Playbook{
		ID: "insider-buys-pb-v1", Study: "insider-buys-v1",
		Rules: []playbook.Rule{{
			ID: "buy-insider-buys", Types: []string{"insider_buy"}, Action: playbook.Buy,
			Evidence: playbook.Evidence{Sessions: 20, MinEvents: 50, Direction: "up", MinEdge: 0.01},
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
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	rev, existed, unchanged, err := labRegister(ctx, a, recLabPlaybook, p.ID, body)
	if err != nil {
		return err
	}
	switch {
	case unchanged:
		fmt.Fprintf(a.stdout, "playbook %s is already registered with this content (revision %d, hash %s).\n", p.ID, rev, p.Hash()[:12])
	case existed:
		fmt.Fprintf(a.stdout, "playbook %s registered as revision %d, a new version of the rules (hash %s).\n", p.ID, rev, p.Hash()[:12])
	default:
		fmt.Fprintf(a.stdout, "playbook %s registered as revision %d (hash %s).\n", p.ID, rev, p.Hash()[:12])
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	if _, err := rs.Latest(ctx, recLabStudySpec, p.Study); errors.Is(err, store.ErrNoRecord) {
		fmt.Fprintf(a.stdout, "study %s is not registered yet.\n", p.Study)
	} else if err != nil {
		return err
	}
	return nil
}

func labLatestPlaybook(ctx context.Context, a *app, id string) (playbook.Playbook, int, error) {
	rs, err := a.records()
	if err != nil {
		return playbook.Playbook{}, 0, err
	}
	rec, err := rs.Latest(ctx, recLabPlaybook, id)
	if errors.Is(err, store.ErrNoRecord) {
		return playbook.Playbook{}, 0, fmt.Errorf("no playbook %s: register it first (playbook register -file F)", id)
	}
	if err != nil {
		return playbook.Playbook{}, 0, err
	}
	var p playbook.Playbook
	if err := json.Unmarshal(rec.Body, &p); err != nil {
		return playbook.Playbook{}, 0, fmt.Errorf("playbook %s does not decode: %w", id, err)
	}
	hist, err := rs.History(ctx, recLabPlaybook, id)
	if err != nil {
		return playbook.Playbook{}, 0, err
	}
	return p, len(hist), nil
}

func labPlaybookList(ctx context.Context, a *app, args []string) error {
	rest, err := parseFlags(newFlags("playbook list"), args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("playbook list takes no arguments, got %q", rest[0])
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	cur, err := rs.Current(ctx, recLabPlaybook, "")
	if err != nil {
		return err
	}
	if len(cur) == 0 {
		fmt.Fprintln(a.stdout, "no playbook is registered (playbook example, playbook register).")
		return nil
	}
	t := cliTable{Header: []string{"playbook", "study", "rules", "revisions", "hash"}, Right: []bool{2: true, 3: true}}
	for _, r := range cur {
		var p playbook.Playbook
		if err := json.Unmarshal(r.Body, &p); err != nil {
			return fmt.Errorf("playbook %s does not decode: %w", r.Key, err)
		}
		hist, err := rs.History(ctx, recLabPlaybook, r.Key)
		if err != nil {
			return err
		}
		t.Rows = append(t.Rows, []cliCell{styled(p.ID, cliStrong), plain(p.Study), plain(fmt.Sprint(len(p.Rules))), plain(fmt.Sprint(len(hist))), plain(p.Hash()[:12])})
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
	p, rev, err := labLatestPlaybook(ctx, a, pos[0])
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
	daysSet := false
	fs.Visit(func(f *flag.Flag) { daysSet = daysSet || f.Name == "days" })
	now := a.now()
	start, end := now.AddDate(0, 0, -*days), now
	switch {
	case *from != "" || *to != "":
		if *from == "" || *to == "" || daysSet {
			return usageErrorf("give either -days or both -from and -to")
		}
		if start, err = parseDay(*from, a.location()); err != nil {
			return usageErrorf("-from: %v", err)
		}
		last, err := parseDay(*to, a.location())
		if err != nil {
			return usageErrorf("-to: %v", err)
		}
		if end = labEndOfDay(last); end.Before(start) {
			return usageErrorf("-to is before -from")
		}
	case *days < 1:
		return usageErrorf("-days must be at least 1")
	}
	p, _, err := labLatestPlaybook(ctx, a, pos[0])
	if err != nil {
		return err
	}
	runs, err := labRuns(ctx, a, p.Study)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("study %s has not been run: run `stoxicity study run %s` first", p.Study, p.Study)
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	signals, err := research.Signals(ctx, db, research.SignalInput{Playbook: p, Report: runs[len(runs)-1].Report, From: start, To: end, Sectors: a.sectorMap()})
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
		rs, err := a.records()
		if err != nil {
			return err
		}
		in := make([]store.RecordInput, 0, len(signals))
		for _, s := range signals {
			body, err := json.Marshal(labSaved{Signal: s, Decision: decisions[s.ID]})
			if err != nil {
				return err
			}
			in = append(in, store.RecordInput{Kind: recLabSignal, Key: s.ID, Body: body})
		}
		if len(in) > 0 {
			if _, fresh, err = rs.Append(ctx, in...); err != nil {
				return err
			}
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
	rs, err := a.records()
	if err != nil {
		return err
	}
	recs, err := rs.Current(ctx, recLabSignal, "")
	if err != nil {
		return err
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Seq > recs[j].Seq })
	var saved []labSaved
	var signals []playbook.Signal
	decisions := map[string]playbook.Decision{}
	for _, r := range recs {
		var x labSaved
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return fmt.Errorf("saved signal %s does not decode: %w", r.Key, err)
		}
		if len(pos) == 1 && x.Signal.Playbook != pos[0] {
			continue
		}
		saved = append(saved, x)
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
