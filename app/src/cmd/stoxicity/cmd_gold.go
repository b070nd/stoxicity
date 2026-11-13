package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/gold"
)

// goldFormat is the label file format, with a one-line example; it is shown by
// the usage text of the gold command.
const goldFormat = `files are JSON lines: a header line, then one item per line; label an item by filling labeller and records ` +
	`(type "none" alone when the text states no economic event; a fact's span is the verbatim words, its unit "USD", "USD/share", "shares", "ratio", "years" or "count"; ` +
	`labels hold the words of label fields); example: ` +
	`{"id":"ev1","symbol":"ACME","kind":"buyback","category":"buyback","published":"2026-11-03T14:00:00Z",` +
	`"text":[{"ref":"a1","text":"Acme announced a $5 billion share repurchase program."}],"labeller":"jd",` +
	`"records":[{"type":"buyback_announced","facts":[{"name":"amount","value":5000000000,"unit":"USD","span":"$5 billion"}],"labels":{"kind":"authorization"}}]}`

// goldDefaultDays is how far back gold sample looks for events.
const goldDefaultDays = 365

// cmdGold builds the labelled test set that precedes any language model and
// scores the existing rules against it.
func cmdGold(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: sample | check | split | score; %s", goldFormat)
	}
	switch args[0] {
	case "sample":
		return goldSample(ctx, a, args[1:])
	case "check":
		return goldCheck(a, args[1:])
	case "split":
		return goldSplit(a, args[1:])
	case "score":
		return goldScore(a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (sample, check, split, score)", args[0])
}

// goldExtractor returns the extractor of the name; the one place a model
// adapter would be added.
func goldExtractor(name string) (gold.Extractor, error) {
	if name == eventfact.TextExtractor {
		return eventfact.FromEvent, nil
	}
	return nil, usageErrorf("unknown extractor %q (%s)", name, eventfact.TextExtractor)
}

func readGold(path string) (gold.File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return gold.File{}, err
	}
	defer fh.Close()
	f, err := gold.Read(fh)
	if err != nil {
		return gold.File{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// writeNewGold writes a gold file to a path that does not exist yet and never
// replaces a file: a labelled file is hours of work and the test part is held out.
func writeNewGold(path string, f gold.File) error {
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; gold never overwrites a file", path)
		}
		return err
	}
	if err := gold.Write(fh, f); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}

func goldSample(ctx context.Context, a *app, args []string) error {
	fs := newFlags("gold sample")
	n := fs.Int("n", 0, "how many events to draw")
	seed := fs.Int64("seed", 1, "seed of the order events are drawn in")
	category := fs.String("category", "", "draw only this category (analyst, buyback, contract, earnings, guidance); empty draws from all")
	out := fs.String("out", "", "file to write; it must not exist")
	days := fs.Int("days", goldDefaultDays, "read the stored events of the last N days")
	exclude := fs.String("exclude", "", "gold files (comma separated) whose events are not drawn again")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	switch {
	case len(rest) > 0:
		return usageErrorf("gold sample takes no arguments, got %q", rest[0])
	case *n < 1:
		return usageErrorf("-n must be at least 1")
	case *out == "":
		return usageErrorf("-out is required")
	case *days < 1:
		return usageErrorf("-days must be at least 1")
	}
	already := map[string]bool{}
	for _, p := range strings.Split(*exclude, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		f, err := readGold(p)
		if err != nil {
			return err
		}
		for _, it := range f.Items {
			already[it.ID] = true
		}
	}
	a.bootstrap() // only the draw reads the environment and the database; the other subcommands are pure over files
	if err := a.startupError(); err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	to := a.now()
	from := to.AddDate(0, 0, -*days)
	cands, err := gold.Candidates(ctx, db, from, to)
	if err != nil {
		return err
	}
	f, err := gold.Draw(cands, gold.Rule{N: *n, Seed: *seed, Category: *category, From: from, To: to}, already)
	if err != nil {
		return err
	}
	if err := writeNewGold(*out, f); err != nil {
		return err
	}
	d := f.Header.Drawn
	fmt.Fprintf(a.stdout, "drew %d of the %d events asked for (%d eligible, %d left out as already drawn) into %s, seed %d\nlabel them by hand, then run gold check\n",
		len(f.Items), d.N, d.Eligible, d.Excluded, *out, d.Seed)
	return nil
}

func goldCheck(a *app, args []string) error {
	fs := newFlags("gold check")
	file := fs.String("file", "", "gold file")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: gold check -file FILE")
	}
	f, err := readGold(*file)
	if err != nil {
		return err
	}
	c := gold.Count(f)
	problems := gold.Check(f)
	for _, p := range problems {
		fmt.Fprintln(a.stderr, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(a.stderr, "stoxicity: gold check: %d problems in %s (%d items, %d labelled)\n", len(problems), *file, c.Items, c.Labelled)
		return errReported
	}
	fmt.Fprintf(a.stdout, "%s: %d items, %d labelled, %d not labelled yet; no problems\n", *file, c.Items, c.Labelled, c.Items-c.Labelled)
	return nil
}

func goldSplit(a *app, args []string) error {
	fs := newFlags("gold split")
	file := fs.String("file", "", "labelled gold file")
	frac := fs.Float64("dev-fraction", 0, "share of items for development, above 0 and below 1")
	seed := fs.Int64("seed", 1, "seed of the split")
	outDev := fs.String("out-dev", "", "development file to write; it must not exist")
	outTest := fs.String("out-test", "", "held-out test file to write; it must not exist")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" || *outDev == "" || *outTest == "" {
		return usageErrorf("usage: gold split -file FILE -dev-fraction F -seed S -out-dev FILE -out-test FILE")
	}
	for _, p := range []string{*outTest, *outDev} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists; a split is made once and gold never overwrites a part", p)
		}
	}
	f, err := readGold(*file)
	if err != nil {
		return err
	}
	dev, test, err := gold.Split(f, *seed, *frac)
	if err != nil {
		return err
	}
	if err := writeNewGold(*outTest, test); err != nil {
		return err
	}
	if err := writeNewGold(*outDev, dev); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "split %d items by seed %d: %d development in %s, %d held out in %s\nscore the test part only as the final reading (gold score -final)\n",
		len(f.Items), *seed, len(dev.Items), *outDev, len(test.Items), *outTest)
	return nil
}

func goldScore(a *app, args []string) error {
	fs := newFlags("gold score")
	file := fs.String("file", "", "gold file")
	name := fs.String("extractor", eventfact.TextExtractor, "the rules to score")
	final := fs.Bool("final", false, "the file is the held-out test part and this is its final reading")
	asJSON := fs.Bool("json", false, "print as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: gold score -file FILE [-extractor %s] [-final] [-json]", eventfact.TextExtractor)
	}
	extract, err := goldExtractor(*name)
	if err != nil {
		return err
	}
	f, err := readGold(*file)
	if err != nil {
		return err
	}
	rep, err := gold.Score(f, *name, extract, *final)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, rep)
	}
	printGoldReport(a.stdout, rep, useColor(a.stdout))
	return nil
}

// goldRate is a precision, recall or agreement as a decimal; n/a when it has
// nothing to divide.
func goldRate(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *v)
}

func printGoldReport(w io.Writer, rep gold.Report, color bool) {
	role := ""
	if rep.Role != "" {
		role = ", " + rep.Role + " part"
	}
	fmt.Fprintf(w, "extractor %s (%s)%s: %d items, %d labelled", rep.Extractor, rep.Version, role, rep.Items, rep.Labelled)
	if left := rep.Items - rep.Labelled; left > 0 {
		fmt.Fprintf(w, ", %d not labelled and left out", left)
	}
	fmt.Fprintf(w, "\nitems labelled none: %d; of them the extractor made a record for %d (false events)\n", rep.NoneItems, rep.FalseEvents)
	if len(rep.FalseEventItems) > 0 {
		fmt.Fprintf(w, "false events: %s\n", strings.Join(rep.FalseEventItems, ", "))
	}

	types := cliTable{Header: []string{"type", "expected", "predicted", "matched", "precision", "recall"}, Right: []bool{false, true, true, true, true, true}}
	for _, t := range rep.Types {
		types.Rows = append(types.Rows, []cliCell{plain(t.Type), plain(fmt.Sprint(t.Expected)), plain(fmt.Sprint(t.Predicted)), plain(fmt.Sprint(t.Matched)),
			plain(goldRate(t.Precision)), plain(goldRate(t.Recall))})
	}
	fmt.Fprintln(w)
	types.write(w, color)

	fields := cliTable{Header: []string{"type", "field", "expected", "predicted", "both", "precision", "recall", "exact"}, Right: []bool{false, false, true, true, true, true, true, true}}
	for _, f := range rep.Fields {
		name := f.Name
		if f.Label {
			name += " (label)"
		}
		fields.Rows = append(fields.Rows, []cliCell{plain(f.Type), plain(name), plain(fmt.Sprint(f.Expected)), plain(fmt.Sprint(f.Predicted)), plain(fmt.Sprint(f.Both)),
			plain(goldRate(f.Precision)), plain(goldRate(f.Recall)), plain(goldRate(f.Exact))})
	}
	fmt.Fprintln(w)
	fields.write(w, color)
	fmt.Fprintln(w, "\nprecision is matched or both over predicted, recall over expected, exact is the share of figures both sides have whose value agrees; derived figures are not counted")
}
