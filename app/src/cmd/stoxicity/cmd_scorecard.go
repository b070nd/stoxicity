package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/scorecard"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	recDatum    = "sc.datum"
	recTemplate = "sc.template"
	recNote     = "sc.note"
)

// cmdScorecard keeps the reported figures and qualitative notes of securities
// and shows them as a multi-year scorecard. It fetches nothing: figures come
// from files you supply, each with its source and reference.
func cmdScorecard(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | import | template | annotate | show | versions")
	}
	switch args[0] {
	case "example":
		fmt.Fprint(a.stdout, scorecard.ExampleCSV)
		return nil
	case "import":
		return scorecardImport(ctx, a, args[1:])
	case "template":
		return scorecardTemplate(ctx, a, args[1:])
	case "annotate":
		return scorecardAnnotate(ctx, a, args[1:])
	case "show":
		return scorecardShow(ctx, a, args[1:])
	case "versions":
		v := scorecard.CurrentVersions()
		fmt.Fprintf(a.stdout, "extractor  %s\nrules      %s\nkeywords   %s\n", v.Extractor, v.Rules, v.Keywords)
		return nil
	}
	return usageErrorf("unknown subcommand %q (example, import, template, annotate, show, versions)", args[0])
}

func (a *app) records() (store.Records, error) {
	if _, err := a.requireTenant(); err != nil {
		return store.Records{}, err
	}
	db, err := a.store()
	if err != nil {
		return store.Records{}, err
	}
	return db.Records(a.tenantID), nil
}

func scorecardImport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("scorecard import")
	file := fs.String("file", "", "CSV: security, metric, end, value, source, ref; optional start, currency, filed, note")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: scorecard import -file FILE (see scorecard example)")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	ds, err := scorecard.ParseCSV(f)
	if err != nil {
		return err
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	for _, d := range ds {
		if err := knownSecurity(secs, d.Security); err != nil {
			return err
		}
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	in := make([]store.RecordInput, 0, len(ds))
	for _, d := range ds {
		body, _ := json.Marshal(d)
		in = append(in, store.RecordInput{Kind: recDatum, Key: d.Key(), Body: body})
	}
	_, fresh, err := rs.Append(ctx, in...)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d figures read, %d new, %d already stored\n", len(ds), fresh, len(ds)-fresh)
	return nil
}

func scorecardTemplate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("scorecard template")
	sec := fs.String("security", "", "the security ID")
	name := fs.String("template", "", "the kind of business")
	list := fs.Bool("list", false, "list the templates")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if *list {
		t := cliTable{Header: []string{"template", "ratios", "what it is"}}
		for _, x := range scorecard.Templates() {
			r := strings.Join(x.Ratios, ", ")
			if r == "" {
				r = "none"
			}
			t.Rows = append(t.Rows, []cliCell{styled(x.Name, cliStrong), plain(r), plain(x.Summary)})
		}
		t.write(a.stdout, useColor(a.stdout))
		return nil
	}
	if *sec == "" || *name == "" {
		return usageErrorf("usage: scorecard template -security S -template NAME | -list")
	}
	if _, ok := scorecard.TemplateByName(*name); !ok {
		return usageErrorf("unknown template %q: see scorecard template -list", *name)
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	if err := knownSecurity(secs, *sec); err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recTemplate, Key: *sec, Body: []byte(`{"template":"` + *name + `"}`)}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s is read as a %s\n", *sec, *name)
	return nil
}

func scorecardAnnotate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("scorecard annotate")
	sec := fs.String("security", "", "the security ID")
	topic := fs.String("topic", "", strings.Join(scorecard.Topics, ", "))
	stance := fs.String("stance", "neutral", "supports, counter or neutral")
	text := fs.String("text", "", "the note")
	source := fs.String("source", "", "who said it")
	ref := fs.String("ref", "", "the document or page")
	at := fs.String("at", "", "the day of the note, YYYY-MM-DD; default today")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	when := a.now().UTC().Truncate(24 * time.Hour)
	if *at != "" {
		if when, err = time.Parse(time.DateOnly, *at); err != nil {
			return usageErrorf("-at must be YYYY-MM-DD")
		}
	}
	n := scorecard.Annotation{Security: *sec, Topic: *topic, Stance: scorecard.Stance(*stance), Text: *text, Source: *source, Ref: *ref, At: when}
	if err := n.Validate(); err != nil {
		return usageErrorf("%v", err)
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	if err := knownSecurity(secs, n.Security); err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(n.Security + "|" + n.Topic + "|" + n.Text + "|" + n.Source + "|" + n.Ref + "|" + when.Format(time.DateOnly)))
	body, _ := json.Marshal(n)
	if _, fresh, err := rs.Append(ctx, store.RecordInput{Kind: recNote, Key: n.Security + "|" + hex.EncodeToString(sum[:6]), Body: body}); err != nil {
		return err
	} else if fresh == 0 {
		fmt.Fprintln(a.stdout, "already recorded")
		return nil
	}
	fmt.Fprintf(a.stdout, "noted on %s (%s, %s); flags: %s\n", n.Security, n.Topic, n.Stance, orDash(strings.Join(scorecard.Flags(n.Text), ", ")))
	return nil
}

// loadCard reads a security's figures and notes and builds its scorecard.
func loadCard(ctx context.Context, rs store.Records, security string, known time.Time, years int) (scorecard.Card, error) {
	recs, err := rs.All(ctx, recDatum, security+"|")
	if err != nil {
		return scorecard.Card{}, err
	}
	var ds []scorecard.Datum
	for _, r := range recs {
		var d scorecard.Datum
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return scorecard.Card{}, fmt.Errorf("stored figure %s: %w", r.Key, err)
		}
		ds = append(ds, d)
	}
	tpl := "unknown"
	if r, err := rs.Latest(ctx, recTemplate, security); err == nil {
		var v struct{ Template string }
		_ = json.Unmarshal(r.Body, &v)
		tpl = v.Template
	}
	notes, err := rs.All(ctx, recNote, security+"|")
	if err != nil {
		return scorecard.Card{}, err
	}
	var ns []scorecard.Annotation
	for _, r := range notes {
		var n scorecard.Annotation
		if err := json.Unmarshal(r.Body, &n); err != nil {
			return scorecard.Card{}, fmt.Errorf("stored note %s: %w", r.Key, err)
		}
		if !known.IsZero() && n.At.After(known) {
			continue
		}
		ns = append(ns, n)
	}
	return scorecard.Build(scorecard.Input{Security: security, Template: tpl, Data: ds, Notes: ns, KnownAt: known, Years: years})
}

func cellText(c scorecard.Cell) cliCell {
	if c.Has() {
		return plain(c.Value.String())
	}
	st := cliYellow
	if c.Status == scorecard.Unsupported {
		st = cliDim
	}
	return styled(strings.ReplaceAll(string(c.Status), "_", " "), st)
}

func scorecardShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("scorecard show")
	sec := fs.String("security", "", "the security ID")
	asOf := fs.String("as-of", "", "only figures filed by this day, YYYY-MM-DD; default everything")
	years := fs.Int("years", 5, "fiscal years to show")
	asJSON := fs.Bool("json", false, "print as JSON")
	reasons := fs.Bool("reasons", false, "explain every cell that has no value")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *sec == "" {
		return usageErrorf("usage: scorecard show -security S [-as-of DAY] [-years N] [-json] [-reasons]")
	}
	var known time.Time
	if *asOf != "" {
		if known, err = time.Parse(time.DateOnly, *asOf); err != nil {
			return usageErrorf("-as-of must be YYYY-MM-DD")
		}
		known = known.Add(24*time.Hour - time.Nanosecond)
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	card, err := loadCard(ctx, rs, *sec, known, *years)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, card)
	}
	if len(card.Years) == 0 {
		fmt.Fprintf(a.stdout, "%s: no annual figures are stored\nimport some with: stoxicity scorecard import -file FILE (scorecard example shows the format)\n", *sec)
		return nil
	}
	fmt.Fprintf(a.stdout, "%s read as a %s; card %s\n\n", card.Security, card.Template, card.Hash[:16])
	head := []string{"year end"}
	right := []bool{false}
	for _, y := range card.Years {
		head = append(head, y.End)
		right = append(right, true)
	}
	t := cliTable{Header: head, Right: right}
	add := func(label string, get func(scorecard.Year) scorecard.Cell, style cliStyle) {
		row := []cliCell{styled(label, style)}
		for _, y := range card.Years {
			row = append(row, cellText(get(y)))
		}
		t.Rows = append(t.Rows, row)
	}
	for _, m := range scorecard.Metrics() {
		m := m
		have := false
		for _, y := range card.Years {
			have = have || y.Raw[m].Has()
		}
		if have {
			add(m, func(y scorecard.Year) scorecard.Cell { return y.Raw[m] }, cliPlain)
		}
	}
	for _, r := range scorecard.Ratios {
		r := r
		add(r, func(y scorecard.Year) scorecard.Cell { return y.Ratios[r] }, cliStrong)
	}
	t.write(a.stdout, useColor(a.stdout))
	if *reasons {
		fmt.Fprintln(a.stdout)
		for _, r := range scorecard.Ratios {
			for _, y := range card.Years {
				if c := y.Ratios[r]; !c.Has() {
					fmt.Fprintf(a.stdout, "%s %s: %s: %s\n", y.End, r, c.Status, c.Reason)
				}
			}
		}
	}
	if len(card.Gaps) > 0 {
		fmt.Fprintln(a.stdout, "\nmissing from the newest year:")
		sort.Strings(card.Gaps)
		for _, g := range card.Gaps {
			fmt.Fprintln(a.stdout, "  "+g)
		}
	}
	for _, n := range card.Notes {
		fl := ""
		if len(n.Flags) > 0 {
			fl = " [" + strings.Join(n.Flags, ", ") + "]"
		}
		fmt.Fprintf(a.stdout, "\n%s %s/%s: %s%s (%s, %s)", n.At.Format(time.DateOnly), n.Topic, n.Stance, n.Text, fl, n.Source, n.Ref)
	}
	fmt.Fprintf(a.stdout, "\n\nfigures are exact, in whole currency units; a ratio is blank for a stated reason, never estimated. versions: %s, %s, %s\n", card.Versions.Extractor, card.Versions.Rules, card.Versions.Keywords)
	return nil
}
