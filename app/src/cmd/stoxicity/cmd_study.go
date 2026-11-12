package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/research"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	recLabStudySpec = "study.spec"
	recLabStudyRun  = "study.run"
)

// labRun is what is kept of one run of a study. The outcomes are not kept:
// they can be large, and a run prints them on request.
type labRun struct {
	At       time.Time            `json:"at"`
	Run      int                  `json:"run"`
	SpecHash string               `json:"spec_hash"`
	DataHash string               `json:"data_hash"`
	Manifest eventstudy.Manifest  `json:"manifest"`
	Prices   research.PriceResult `json:"prices"`
	Report   eventstudy.Report    `json:"report"`
}

// cmdStudy runs event studies over the stored economic events and daily bars.
func cmdStudy(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | register | list | show | run | last")
	}
	switch args[0] {
	case "example":
		if len(args) > 1 {
			return usageErrorf("study example takes no arguments")
		}
		return labStudyExample(a)
	case "register":
		return labStudyRegister(ctx, a, args[1:])
	case "list":
		return labStudyList(ctx, a, args[1:])
	case "show":
		return labStudyShow(ctx, a, args[1:])
	case "run":
		return labStudyRun(ctx, a, args[1:])
	case "last":
		return labStudyLast(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (example, register, list, show, run, last)", args[0])
}

func labStudyExample(a *app) error {
	spec := eventstudy.DefaultSpec()
	spec.ID = "insider-buys-v1"
	spec.Types = []string{"insider_buy"}
	spec.Benchmark = "SPY"
	spec.Development = eventstudy.Window{From: "2023-01-01", To: "2024-12-31"}
	spec.Validation = eventstudy.Window{From: "2025-01-01", To: "2025-12-31"}
	fmt.Fprintln(a.stdout, "# study.json: every number is a conservative starting value to review, not a finding. Patterns are looked for in development and confirmed in validation, which is read as rarely as possible.")
	return writeJSON(a.stdout, spec)
}

// labRegister stores body as the newest revision of a key and says which
// revision it is. unchanged is true when the key already holds this body.
func labRegister(ctx context.Context, a *app, kind, key string, body []byte) (revision int, existed, unchanged bool, err error) {
	rs, err := a.records()
	if err != nil {
		return 0, false, false, err
	}
	hist, err := rs.History(ctx, kind, key)
	if err != nil {
		return 0, false, false, err
	}
	if n := len(hist); n > 0 {
		if hist[n-1].Hash == store.RecordHash(body) {
			return n, true, true, nil
		}
	}
	_, fresh, err := rs.Append(ctx, store.RecordInput{Kind: kind, Key: key, Body: body})
	if err != nil {
		return 0, false, false, err
	}
	if fresh == 0 {
		return 0, true, false, fmt.Errorf("%s was registered with exactly this content before and cannot be made the newest revision again: change something, or register it under a new id", key)
	}
	return len(hist) + 1, len(hist) > 0, false, nil
}

func labStudyRegister(ctx context.Context, a *app, args []string) error {
	fs := newFlags("study register")
	file := fs.String("file", "", "study JSON (study example shows one)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: study register -file F")
	}
	var spec eventstudy.Spec
	if err := readJSONFile(*file, &spec); err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	rev, existed, unchanged, err := labRegister(ctx, a, recLabStudySpec, spec.ID, body)
	if err != nil {
		return err
	}
	switch {
	case unchanged:
		fmt.Fprintf(a.stdout, "study %s is already registered with this content (revision %d, hash %s).\n", spec.ID, rev, spec.Hash()[:12])
	case existed:
		fmt.Fprintf(a.stdout, "study %s registered as revision %d: a new revision of a study that was tried before (hash %s).\n", spec.ID, rev, spec.Hash()[:12])
	default:
		fmt.Fprintf(a.stdout, "study %s registered (hash %s).\n", spec.ID, spec.Hash()[:12])
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	cur, err := rs.Current(ctx, recLabStudySpec, "")
	if err != nil {
		return err
	}
	all, err := rs.All(ctx, recLabStudySpec, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d study(ies) and %d revision(s) are registered. Every study and every revision is another look at the same history; the more that is tried, the more a result needs.\n", len(cur), len(all))
	return nil
}

// labLatestSpec is the newest revision of a registered study and its number.
func labLatestSpec(ctx context.Context, a *app, id string) (eventstudy.Spec, int, error) {
	rs, err := a.records()
	if err != nil {
		return eventstudy.Spec{}, 0, err
	}
	rec, err := rs.Latest(ctx, recLabStudySpec, id)
	if errors.Is(err, store.ErrNoRecord) {
		return eventstudy.Spec{}, 0, fmt.Errorf("no study %s: register it first (study register -file F)", id)
	}
	if err != nil {
		return eventstudy.Spec{}, 0, err
	}
	var spec eventstudy.Spec
	if err := json.Unmarshal(rec.Body, &spec); err != nil {
		return eventstudy.Spec{}, 0, fmt.Errorf("study %s does not decode: %w", id, err)
	}
	hist, err := rs.History(ctx, recLabStudySpec, id)
	if err != nil {
		return eventstudy.Spec{}, 0, err
	}
	return spec, len(hist), nil
}

// labRuns lists the stored runs of a study, oldest first.
func labRuns(ctx context.Context, a *app, id string) ([]labRun, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	recs, err := rs.History(ctx, recLabStudyRun, id)
	if err != nil {
		return nil, err
	}
	out := make([]labRun, 0, len(recs))
	for _, r := range recs {
		var x labRun
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return nil, fmt.Errorf("a run of study %s does not decode: %w", id, err)
		}
		out = append(out, x)
	}
	return out, nil
}

func labWindow(w eventstudy.Window) string {
	if w.From == "" && w.To == "" {
		return "none"
	}
	return w.From + ".." + w.To
}

func labStudyList(ctx context.Context, a *app, args []string) error {
	rest, err := parseFlags(newFlags("study list"), args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("study list takes no arguments, got %q", rest[0])
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	cur, err := rs.Current(ctx, recLabStudySpec, "")
	if err != nil {
		return err
	}
	if len(cur) == 0 {
		fmt.Fprintln(a.stdout, "no study is registered (study example, study register).")
		return nil
	}
	t := cliTable{Header: []string{"study", "types", "horizons", "development", "validation", "revisions", "runs", "last run"}, Right: []bool{5: true, 6: true}}
	for _, r := range cur {
		var spec eventstudy.Spec
		if err := json.Unmarshal(r.Body, &spec); err != nil {
			return fmt.Errorf("study %s does not decode: %w", r.Key, err)
		}
		hist, err := rs.History(ctx, recLabStudySpec, r.Key)
		if err != nil {
			return err
		}
		runs, err := labRuns(ctx, a, r.Key)
		if err != nil {
			return err
		}
		var hs []string
		for _, h := range spec.Horizons {
			hs = append(hs, fmt.Sprint(h))
		}
		last := plain("not run")
		if n := len(runs); n > 0 {
			st := runs[n-1].Report.Status
			last = styled(st, studyStyle(st))
		}
		t.Rows = append(t.Rows, []cliCell{styled(r.Key, cliStrong), plain(strings.Join(spec.Types, ",")), plain(strings.Join(hs, ",")),
			plain(labWindow(spec.Development)), plain(labWindow(spec.Validation)), plain(fmt.Sprint(len(hist))), plain(fmt.Sprint(len(runs))), last})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func labStudyShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("study show")
	asJSON := fs.Bool("json", false, "print the JSON alone")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: study show ID [-json]")
	}
	spec, rev, err := labLatestSpec(ctx, a, pos[0])
	if err != nil {
		return err
	}
	if !*asJSON {
		fmt.Fprintf(a.stdout, "# study %s, revision %d, hash %s\n", spec.ID, rev, spec.Hash()[:12])
	}
	return writeJSON(a.stdout, spec)
}

func labStudyRun(ctx context.Context, a *app, args []string) error {
	fs := newFlags("study run")
	synthetic := fs.Bool("synthetic", false, "claim: the events or prices are invented (a claim of yours the program cannot check)")
	pit := fs.Bool("point-in-time", false, "claim: the events came from every security that existed then (a claim of yours the program cannot check)")
	delisted := fs.Bool("with-delisted", false, "claim: the prices include securities that stopped trading (a claim of yours the program cannot check)")
	at := fs.String("at", "", "read the events and bars as held by this time (RFC 3339)")
	breakdown := fs.Bool("breakdown", false, "also print the groups by market condition")
	outcomes := fs.Bool("outcomes", false, "also list every outcome")
	asJSON := fs.Bool("json", false, "print as JSON")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: study run ID [-synthetic] [-point-in-time] [-with-delisted] [-at TIME] [-breakdown] [-outcomes] [-json]")
	}
	in := research.StudyInput{Manifest: eventstudy.Manifest{Synthetic: *synthetic, PointInTimeUniverse: *pit, IncludesDelisted: *delisted}, Sectors: a.sectorMap()}
	if *at != "" {
		if in.AsOf, err = time.Parse(time.RFC3339, *at); err != nil {
			return usageErrorf("-at %q is not an RFC 3339 time such as 2026-11-03T10:00:00Z", *at)
		}
	}
	spec, _, err := labLatestSpec(ctx, a, pos[0])
	if err != nil {
		return err
	}
	in.Spec = spec
	earlier, err := labRuns(ctx, a, spec.ID)
	if err != nil {
		return err
	}
	reads, first := 0, time.Time{}
	for _, r := range earlier {
		if r.Report.Spec.Validation.From != "" || r.Report.Spec.Validation.To != "" {
			if reads == 0 {
				first = r.At
			}
			reads++
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	report, prices, err := research.Study(ctx, db, in)
	if err != nil {
		return err
	}
	kept := report
	kept.Outcomes = nil
	run := labRun{At: a.now().UTC(), Run: len(earlier) + 1, SpecHash: report.SpecHash, DataHash: report.DataHash, Manifest: report.Manifest, Prices: prices, Report: kept}
	body, err := json.Marshal(run)
	if err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recLabStudyRun, Key: spec.ID, Body: body}); err != nil {
		return err
	}
	if !*outcomes {
		report.Outcomes = nil
	}
	if *asJSON {
		return writeJSON(a.stdout, map[string]any{"run": run.Run, "earlier_validation_reads": reads, "prices": prices, "report": report})
	}
	if reads > 0 {
		fmt.Fprintf(a.stdout, "The validation window of this study has been read %d time(s) before, first on %s. A pattern chosen after looking at validation is not confirmed by it.\n", reads, first.In(a.location()).Format(time.DateOnly))
	}
	printStudyReport(a, report, prices, *breakdown, *outcomes)
	return nil
}

func labStudyLast(ctx context.Context, a *app, args []string) error {
	fs := newFlags("study last")
	breakdown := fs.Bool("breakdown", false, "also print the groups by market condition")
	asJSON := fs.Bool("json", false, "print as JSON")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: study last ID [-breakdown] [-json]")
	}
	runs, err := labRuns(ctx, a, pos[0])
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("study %s has not been run: run `stoxicity study run %s` first", pos[0], pos[0])
	}
	last := runs[len(runs)-1]
	if *asJSON {
		return writeJSON(a.stdout, last)
	}
	fmt.Fprintf(a.stdout, "run %d of study %s, at %s\n", last.Run, pos[0], last.At.In(a.location()).Format(time.RFC3339))
	printStudyReport(a, last.Report, last.Prices, *breakdown, false)
	return nil
}
