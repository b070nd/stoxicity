package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstrat"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/walkfwd"
)

const (
	recWFTrial    = "walkfwd.trial"
	recWFSplits   = "walkfwd.splits"
	recWFCriteria = "walkfwd.criteria"
	recWFFreeze   = "walkfwd.freeze"
	recWFRun      = "walkfwd.run"
)

// cmdWalkfwd replays proposals against finished bars under preregistered criteria.
func cmdWalkfwd(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | register | splits | criteria | freeze | run | status | from-events")
	}
	switch args[0] {
	case "example":
		return wfExample(a)
	case "register":
		return wfRegister(ctx, a, args[1:])
	case "splits":
		return wfStore(ctx, a, args[1:], recWFSplits, "splits", func(path string) (any, error) {
			var s walkfwd.Splits
			if err := readJSONFile(path, &s); err != nil {
				return nil, err
			}
			return s, s.Validate()
		})
	case "criteria":
		return wfStore(ctx, a, args[1:], recWFCriteria, "criteria", func(path string) (any, error) {
			var c walkfwd.Criteria
			if err := readJSONFile(path, &c); err != nil {
				return nil, err
			}
			return c, c.Validate()
		})
	case "freeze":
		return wfFreeze(ctx, a)
	case "run":
		return wfRun(ctx, a, args[1:])
	case "status":
		return wfStatus(ctx, a, args[1:])
	case "from-events":
		return wfFromEvents(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q", args[0])
}

func wfExample(a *app) error {
	say := func(s string, v any) {
		fmt.Fprintln(a.stdout, "# "+s)
		_ = writeJSON(a.stdout, v)
	}
	say("trial.json: every strategy and parameter family you try, before you run it. The count of trials raises the evidence each result needs.", walkfwd.Trial{ID: "earnings-v1-a", Strategy: "earnings-followthrough-paper-v1", Params: "lookback=20 volume=1.5"})
	say("splits.json: frozen, in order, separated by an embargo at least as long as the longest holding horizon; the reserve is read once.", walkfwd.Splits{
		Train: walkfwd.Span{From: "2019-01-01", To: "2020-12-31"}, Tune: walkfwd.Span{From: "2021-02-01", To: "2021-12-31"},
		Test: walkfwd.Span{From: "2022-02-01", To: "2022-12-31"}, Reserve: walkfwd.Span{From: "2023-02-01", To: "2023-12-31"}, EmbargoDays: 30})
	say("criteria.json: stated before the test period is read.", walkfwd.Criteria{MinMatured: 100, MinClusters: 30, MinCoverage: 0.8, MinFillRate: 0.5, MinExcessLower: 0, MaxWorstLoss: 0.3, Alpha: 0.05})
	say("manifest.json: what the data is. Anything not true here caps the status.", walkfwd.Manifest{Kind: walkfwd.Known, PointInTime: false, IncludesDelisted: false, Fund: "REPLACE: the diversified fund's security id in the bars"})
	say("proposals.json (decided and known_on are days; qty 0 is a decision not to trade and is kept):", []walkfwd.Proposal{{ID: "p1", Trial: "earnings-v1-a", Security: "sec_x", Sector: "tech", Decided: "2022-03-01", KnownOn: "2022-03-01", Limit: money.MustDecimal("100.5"), Qty: money.MustDecimal("10"), HorizonSessions: 10, Currency: "USD"}})
	say("config.json (optional; these are the defaults; fees come from -fees):", walkfwd.DefaultConfig())
	return nil
}

func wfAppend(ctx context.Context, a *app, kind, key string, v any) error {
	return appendHeld(ctx, a, kind, key, v)
}

func wfLatest(ctx context.Context, a *app, kind, key string, v any) (bool, error) {
	rs, err := a.records()
	if err != nil {
		return false, err
	}
	rec, err := rs.Latest(ctx, kind, key)
	if errors.Is(err, store.ErrNoRecord) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(rec.Body, v)
}

func wfFrozen(ctx context.Context, a *app) (string, error) {
	var f struct {
		Hash string `json:"hash"`
	}
	ok, err := wfLatest(ctx, a, recWFFreeze, "current", &f)
	if !ok {
		return "", err
	}
	return f.Hash, err
}

func wfRegister(ctx context.Context, a *app, args []string) error {
	fs := newFlags("walkfwd register")
	file := fs.String("file", "", "trial JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *file == "" {
		return usageErrorf("usage: walkfwd register -file trial.json")
	}
	var t walkfwd.Trial
	if err := readJSONFile(*file, &t); err != nil {
		return err
	}
	if t.ID == "" || t.Strategy == "" || t.Params == "" {
		return errors.New("a trial needs an id, a strategy and its parameters, so that it can be told from another")
	}
	var prev walkfwd.Trial
	if ok, err := wfLatest(ctx, a, recWFTrial, t.ID, &prev); err != nil {
		return err
	} else if ok && prev != t {
		return fmt.Errorf("trial %s is registered with other parameters: a tried family is never edited, register a new id", t.ID)
	}
	if err := wfAppend(ctx, a, recWFTrial, t.ID, t); err != nil {
		return err
	}
	n, err := wfTrials(ctx, a)
	if err != nil {
		return err
	}
	late := ""
	if h, _ := wfFrozen(ctx, a); h != "" {
		late = " It was registered after the freeze and is counted all the same."
	}
	fmt.Fprintf(a.stdout, "trial %s registered; %d trial(s) in all.%s\n", t.ID, len(n), late)
	return nil
}

func wfTrials(ctx context.Context, a *app) ([]walkfwd.Trial, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	recs, err := rs.Current(ctx, recWFTrial, "")
	if err != nil {
		return nil, err
	}
	var out []walkfwd.Trial
	for _, r := range recs {
		var t walkfwd.Trial
		if err := json.Unmarshal(r.Body, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func wfStore(ctx context.Context, a *app, args []string, kind, name string, read func(string) (any, error)) error {
	fs := newFlags("walkfwd " + name)
	file := fs.String("file", "", name+" JSON; without it the stored one is shown")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *file == "" {
		var raw json.RawMessage
		ok, err := wfLatest(ctx, a, kind, "current", &raw)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(a.stdout, "no %s have been stated.\n", name)
			return nil
		}
		return writeJSON(a.stdout, raw)
	}
	if h, err := wfFrozen(ctx, a); err != nil {
		return err
	} else if h != "" {
		return fmt.Errorf("the %s are frozen: they were fixed before the test period was read so that they cannot follow the result", name)
	}
	v, err := read(*file)
	if err != nil {
		return err
	}
	if err := wfAppend(ctx, a, kind, "current", v); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s recorded.\n", name)
	return nil
}

func wfFreeze(ctx context.Context, a *app) error {
	var s walkfwd.Splits
	var c walkfwd.Criteria
	if ok, err := wfLatest(ctx, a, recWFSplits, "current", &s); err != nil || !ok {
		return errors.Join(err, errors.New("state the splits first (walkfwd splits -file F)"))
	}
	if ok, err := wfLatest(ctx, a, recWFCriteria, "current", &c); err != nil || !ok {
		return errors.Join(err, errors.New("state the criteria first (walkfwd criteria -file F)"))
	}
	if h, err := wfFrozen(ctx, a); err != nil {
		return err
	} else if h != "" {
		return errors.New("already frozen")
	}
	b, _ := json.Marshal(struct {
		S walkfwd.Splits
		C walkfwd.Criteria
	}{s, c})
	h := fmt.Sprintf("%x", sha256.Sum256(b))
	if err := wfAppend(ctx, a, recWFFreeze, "current", map[string]string{"hash": h, "at": a.now().UTC().Format(time.RFC3339)}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "splits and criteria frozen (%s). The test period may now be read; the reserve once.\n", h[:12])
	return nil
}

type wfRunRecord struct {
	Trial    string         `json:"trial"`
	Period   walkfwd.Period `json:"period"`
	Status   walkfwd.Status `json:"status"`
	Kind     walkfwd.Kind   `json:"kind"`
	Manifest string         `json:"manifest"`
	At       string         `json:"at"`
	Seq      int            `json:"seq"`
}

func wfRuns(ctx context.Context, a *app) ([]wfRunRecord, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	recs, err := rs.All(ctx, recWFRun, "")
	if err != nil {
		return nil, err
	}
	var out []wfRunRecord
	for _, r := range recs {
		var x wfRunRecord
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

func wfRun(ctx context.Context, a *app, args []string) error {
	fs := newFlags("walkfwd run")
	trial := fs.String("trial", "", "the registered trial")
	period := fs.String("period", "", "train, tune, test or reserve")
	manifest := fs.String("manifest", "", "manifest JSON: what the data is")
	proposals := fs.String("proposals", "", "proposals JSON")
	bars := fs.String("bars", "", "CSV of finished daily bars as traded, including the fund's")
	dists := fs.String("dists", "", "JSON list of distributions per share")
	delist := fs.String("delist", "", "JSON list of delistings")
	cfgFile := fs.String("config", "", "simulation config JSON")
	feesFile := fs.String("fees", "", "fee schedule JSON with its source")
	full := fs.Bool("outcomes", false, "also list every outcome")
	asJSON := fs.Bool("json", false, "print as JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *trial == "" || *period == "" || *manifest == "" || *proposals == "" || *bars == "" {
		return usageErrorf("usage: walkfwd run -trial ID -period P -manifest F -proposals F -bars F [-dists F] [-delist F] [-config F] [-fees F] [-outcomes] [-json]")
	}
	per := walkfwd.Period(*period)
	if per != walkfwd.Train && per != walkfwd.Tune && per != walkfwd.Test && per != walkfwd.Reserve {
		return usageErrorf("-period %q is not train, tune, test or reserve", *period)
	}
	trials, err := wfTrials(ctx, a)
	if err != nil {
		return err
	}
	in := walkfwd.Input{Period: per, Trials: len(trials), Config: walkfwd.DefaultConfig()}
	for _, t := range trials {
		if t.ID == *trial {
			in.Trial = t
		}
	}
	if in.Trial.ID == "" {
		return fmt.Errorf("trial %s is not registered: register it before running it (walkfwd register)", *trial)
	}
	if ok, err := wfLatest(ctx, a, recWFSplits, "current", &in.Splits); err != nil || !ok {
		return errors.Join(err, errors.New("the splits have not been stated"))
	}
	if ok, err := wfLatest(ctx, a, recWFCriteria, "current", &in.Criteria); err != nil || !ok {
		return errors.Join(err, errors.New("the criteria have not been stated"))
	}
	frozen, err := wfFrozen(ctx, a)
	if err != nil {
		return err
	}
	runs, err := wfRuns(ctx, a)
	if err != nil {
		return err
	}
	if per == walkfwd.Test || per == walkfwd.Reserve {
		if frozen == "" {
			return fmt.Errorf("the %s period is read only after the splits and criteria are frozen (walkfwd freeze)", per)
		}
		for _, r := range runs {
			if per == walkfwd.Reserve && r.Period == walkfwd.Reserve {
				return errors.New("the reserve has been read once already: it was kept untouched for one final look")
			}
			if per == walkfwd.Test && r.Period == walkfwd.Test {
				in.TestReads++
			}
		}
	}
	if *cfgFile != "" {
		if err := readJSONFile(*cfgFile, &in.Config); err != nil {
			return err
		}
	}
	if in.Config.Fees, err = readFees(*feesFile, "USD"); err != nil {
		return err
	}
	if err := readJSONFile(*manifest, &in.Manifest); err != nil {
		return err
	}
	if err := readJSONFile(*proposals, &in.Proposals); err != nil {
		return err
	}
	if in.Bars, err = readBars(*bars); err != nil {
		return err
	}
	if *dists != "" {
		if err := readJSONFile(*dists, &in.Dists); err != nil {
			return err
		}
	}
	if *delist != "" {
		if err := readJSONFile(*delist, &in.Delist); err != nil {
			return err
		}
	}
	rep, err := walkfwd.Run(in)
	if err != nil {
		return err
	}
	if per == walkfwd.Test || per == walkfwd.Reserve {
		rec := wfRunRecord{Trial: *trial, Period: per, Status: rep.Status, Kind: rep.Kind, Manifest: rep.Manifest, At: a.now().UTC().Format(time.RFC3339), Seq: len(runs) + 1}
		if err := wfAppend(ctx, a, recWFRun, fmt.Sprintf("%06d", rec.Seq), rec); err != nil {
			return err
		}
	}
	if !*full {
		rep.Outcomes = nil
	}
	if *asJSON {
		return printJSON(a, rep)
	}
	printWalkfwd(a, rep)
	return nil
}

func statusStyle(s walkfwd.Status) cliStyle {
	switch s {
	case walkfwd.Evaluated:
		return cliGreen
	case walkfwd.Experimental:
		return cliYellow
	}
	return cliRed
}

func printWalkfwd(a *app, r walkfwd.Report) {
	color := useColor(a.stdout)
	fmt.Fprintf(a.stdout, "%s  trial %s, %s period, %s replay, %d trial(s) registered, interval at %.2f%%\n",
		styledText(strings.ToUpper(string(r.Status)), statusStyle(r.Status), color), r.Trial, r.Period, r.Kind, r.Trials, 100*r.Level)
	for _, w := range r.Why {
		fmt.Fprintln(a.stdout, "- "+w)
	}
	pc := func(v float64) string { return fmt.Sprintf("%+.2f%%", 100*v) }
	t := cliTable{Header: []string{"measure", "value", "note"}}
	row := func(n, v, note string) {
		t.Rows = append(t.Rows, []cliCell{styled(n, cliStrong), plain(v), plain(note)})
	}
	row("proposals", fmt.Sprint(r.Proposals), fmt.Sprintf("%d no-trade decisions, %d excluded for lookahead, %d unfilled or thin, %d with no data", r.NoTrade, r.Excluded, r.Unfilled, r.MissingData))
	row("filled", fmt.Sprint(r.Filled), fmt.Sprintf("fill rate %.2f, coverage %.2f", r.FillRate, r.Coverage))
	row("matured", fmt.Sprint(r.Matured), fmt.Sprintf("%d censored (no exit in the data), %d delisted, in %d independent clusters", r.Censored, r.Delisted, r.ClusterN))
	row("mean net return", pc(r.Mean), fmt.Sprintf("median %s, hit rate %.2f, worst %s, 5th percentile %s", pc(r.Median), r.HitRate, pc(r.Worst), pc(r.Fifth)))
	if r.Excess != nil {
		row("net excess over the fund", pc(r.Excess.Mean), fmt.Sprintf("interval %s to %s over %d trades in %d clusters", pc(r.Excess.Low), pc(r.Excess.High), r.Excess.N, r.Excess.Clusters))
	} else {
		row("net excess over the fund", "unavailable", "no fund bars cover the trades")
	}
	if r.ExcessCash != nil {
		row("net excess over cash", pc(r.ExcessCash.Mean), fmt.Sprintf("interval %s to %s", pc(r.ExcessCash.Low), pc(r.ExcessCash.High)))
	}
	row("costs and turnover", fmt.Sprint(r.Costs), fmt.Sprintf("fees in minor units; traded %d; held %.1f sessions on average", r.Turnover, r.MeanDays))
	t.write(a.stdout, color)
	sens := func(title string, m map[string]float64) {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var parts []string
		for _, k := range ks {
			parts = append(parts, k+": "+pc(m[k]))
		}
		if len(parts) > 0 {
			fmt.Fprintln(a.stdout, title+" "+strings.Join(parts, ", "))
		}
	}
	sens("mean net return by extra slippage:", r.SlippageSens)
	sens("mean net return by withholding on distributions:", r.WithholdingSens)
	for _, g := range r.ByQuarter {
		fmt.Fprintf(a.stdout, "  %s: %d trades, mean %s\n", g.Name, g.N, pc(g.Mean))
	}
	for _, g := range r.BySector {
		fmt.Fprintf(a.stdout, "  sector %s: %d trades, mean %s\n", g.Name, g.N, pc(g.Mean))
	}
	for _, d := range r.Diagnostics {
		fmt.Fprintln(a.stdout, styledText("bias: ", cliYellow, color)+d)
	}
	fmt.Fprintln(a.stdout, "A profit on synthetic data validates nothing; a result here is evidence about one period, not a forecast. manifest "+r.Manifest[:12])
}

func wfStatus(ctx context.Context, a *app, args []string) error {
	fs := newFlags("walkfwd status")
	asJSON := fs.Bool("json", false, "print as JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	trials, err := wfTrials(ctx, a)
	if err != nil {
		return err
	}
	runs, err := wfRuns(ctx, a)
	if err != nil {
		return err
	}
	frozen, err := wfFrozen(ctx, a)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, map[string]any{"trials": trials, "runs": runs, "frozen": frozen != ""})
	}
	state := "NOT frozen: the test period cannot be read"
	if frozen != "" {
		state = "frozen"
	}
	fmt.Fprintf(a.stdout, "%d trial(s) registered; splits and criteria %s; %d read(s) of test or reserve.\n", len(trials), state, len(runs))
	if len(trials) == 0 {
		return nil
	}
	color := useColor(a.stdout)
	t := cliTable{Header: []string{"trial", "strategy", "parameters", "latest test or reserve result"}}
	for _, tr := range trials {
		last := "not run"
		for _, r := range runs {
			if r.Trial == tr.ID {
				last = fmt.Sprintf("%s on %s (%s replay, run %d)", r.Status, r.Period, r.Kind, r.Seq)
			}
		}
		t.Rows = append(t.Rows, []cliCell{styled(tr.ID, cliStrong), plain(tr.Strategy), plain(tr.Params), plain(last)})
	}
	t.write(a.stdout, color)
	return nil
}

// wfFromEvents turns the saved event-sleeve proposals into replay proposals: a
// PAPER one is a trade, a WAIT or RESEARCH one a decision not to trade, kept.
func wfFromEvents(ctx context.Context, a *app, args []string) error {
	fs := newFlags("walkfwd from-events")
	trial := fs.String("trial", "", "the trial to attribute them to")
	cur := fs.String("currency", "USD", "the currency of the proposals")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *trial == "" {
		return usageErrorf("usage: walkfwd from-events -trial ID")
	}
	recs, err := loadEventRecords(ctx, a)
	if err != nil {
		return err
	}
	var out []walkfwd.Proposal
	for _, r := range recs {
		p := r.Proposal
		w := walkfwd.Proposal{ID: p.Event, Trial: *trial, Security: p.Security, Decided: r.Day, KnownOn: p.EventSession, Currency: money.Currency(*cur), HorizonSessions: p.ReviewSessions}
		if w.KnownOn == "" {
			w.KnownOn = r.Day
		}
		if p.Status == eventstrat.Paper && p.Qty != nil && p.EntryLimit != nil {
			w.Qty, w.Limit, w.Invalidation = *p.Qty, *p.EntryLimit, p.Invalidation
			if p.Notional != nil {
				w.Currency = p.Notional.Currency()
			}
		}
		out = append(out, w)
	}
	return writeJSON(a.stdout, out)
}
