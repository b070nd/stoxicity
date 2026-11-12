package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/research"
)

// lab is the research lab over the tenant's records.
func (a *app) lab() (research.Lab, error) {
	rs, err := a.records()
	if err != nil {
		return research.Lab{}, err
	}
	return research.Lab{Records: rs}, nil
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
	lab, err := a.lab()
	if err != nil {
		return err
	}
	reg, err := lab.RegisterStudy(ctx, spec)
	if err != nil {
		return err
	}
	switch {
	case reg.Unchanged:
		fmt.Fprintf(a.stdout, "study %s is already registered with this content (revision %d, hash %s).\n", spec.ID, reg.Revision, spec.Hash()[:12])
	case reg.Existed:
		fmt.Fprintf(a.stdout, "study %s registered as revision %d: a new revision of a study that was tried before (hash %s).\n", spec.ID, reg.Revision, spec.Hash()[:12])
	default:
		fmt.Fprintf(a.stdout, "study %s registered (hash %s).\n", spec.ID, spec.Hash()[:12])
	}
	studies, revisions, err := lab.StudyCounts(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d study(ies) and %d revision(s) are registered. Every study and every revision is another look at the same history; the more that is tried, the more a result needs.\n", studies, revisions)
	return nil
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
	lab, err := a.lab()
	if err != nil {
		return err
	}
	studies, err := lab.Studies(ctx)
	if err != nil {
		return err
	}
	if len(studies) == 0 {
		fmt.Fprintln(a.stdout, "no study is registered (study example, study register).")
		return nil
	}
	t := cliTable{Header: []string{"study", "types", "horizons", "development", "validation", "revisions", "runs", "last run"}, Right: []bool{5: true, 6: true}}
	for _, st := range studies {
		spec := st.Spec
		var hs []string
		for _, h := range spec.Horizons {
			hs = append(hs, fmt.Sprint(h))
		}
		last := plain("not run")
		if st.Runs > 0 {
			last = styled(st.LastStatus, studyStyle(st.LastStatus))
		}
		t.Rows = append(t.Rows, []cliCell{styled(spec.ID, cliStrong), plain(strings.Join(spec.Types, ",")), plain(strings.Join(hs, ",")),
			plain(labWindow(spec.Development)), plain(labWindow(spec.Validation)), plain(fmt.Sprint(st.Revisions)), plain(fmt.Sprint(st.Runs)), last})
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
	lab, err := a.lab()
	if err != nil {
		return err
	}
	spec, rev, err := lab.LatestStudy(ctx, pos[0])
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
	lab, err := a.lab()
	if err != nil {
		return err
	}
	spec, _, err := lab.LatestStudy(ctx, pos[0])
	if err != nil {
		return err
	}
	in.Spec = spec
	earlier, err := lab.Runs(ctx, spec.ID)
	if err != nil {
		return err
	}
	reads, first := research.ValidationReads(earlier)
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
	run := research.Run{At: a.now().UTC(), Run: len(earlier) + 1, SpecHash: report.SpecHash, DataHash: report.DataHash, Manifest: report.Manifest, Prices: prices, Report: kept}
	if err := lab.RecordRun(ctx, spec.ID, run); err != nil {
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
	lab, err := a.lab()
	if err != nil {
		return err
	}
	runs, err := lab.Runs(ctx, pos[0])
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
