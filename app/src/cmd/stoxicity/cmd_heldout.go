package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/heldout"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	recHeldCorpus     = "heldout.corpus"
	recHeldLabel      = "heldout.label"
	recHeldAdjudicate = "heldout.adjudication"
	recHeldThresholds = "heldout.thresholds"
	recHeldFreeze     = "heldout.freeze"
)

// cmdHeldout is the evaluation of news reading against independent labels.
func cmdHeldout(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | corpus | label | adjudicate | thresholds | freeze | disagreements | evaluate")
	}
	switch args[0] {
	case "example":
		return heldoutExample(a)
	case "corpus":
		return heldoutCorpus(ctx, a, args[1:])
	case "label":
		return heldoutLabel(ctx, a, args[1:])
	case "adjudicate":
		return heldoutAdjudicate(ctx, a, args[1:])
	case "thresholds":
		return heldoutThresholds(ctx, a, args[1:])
	case "freeze":
		return heldoutFreeze(ctx, a)
	case "disagreements":
		return heldoutDisagreements(ctx, a, args[1:])
	case "evaluate":
		return heldoutEvaluate(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q", args[0])
}

func heldoutExample(a *app) error {
	title, summary := "Acme Corp completed its acquisition of Bolt Industries", ""
	c := heldout.Corpus{
		Name:      "REPLACE: a name for this corpus",
		Labellers: []heldout.Labeller{{ID: "REPLACE-1", Independent: false, Note: "set independent true only if this person did not write or tune the rules and had not seen the program's output"}, {ID: "REPLACE-2"}, {ID: "REPLACE-3"}},
		Events:    []heldout.EventDef{{Key: "acme-bolt", Description: "Acme completes its purchase of Bolt"}},
		Names:     map[string][]string{"ACME": {"Acme Corp"}, "BOLT": {"Bolt Industries"}},
		Items: []heldout.Item{{ID: "item-1", SourceID: "REPLACE", URL: "https://example.com/REPLACE", Licence: heldout.Internal, Terms: "https://example.com/terms",
			Published: "2026-11-03", Language: "en", ArticleID: "REPLACE: the article ID in the local store", Hash: heldout.ContentHash(title, summary), Tickers: []string{"ACME", "BOLT"}}},
	}
	fmt.Fprintln(a.stdout, "# corpus.json. Text may be kept in the file only for licence \"redistributable\"; otherwise it is read from the local store by article_id and must match content_hash.")
	if err := writeJSON(a.stdout, c); err != nil {
		return err
	}
	l := []heldout.Record{{Labeller: "REPLACE-1", Item: "item-1", Version: 1, Label: heldout.ItemLabel{Event: "acme-bolt", Claims: []heldout.ClaimLabel{{Kind: "acquisition", Status: "completed", Parties: []heldout.PartyLabel{{Ticker: "ACME", Role: "subject"}, {Ticker: "BOLT", Role: "counterparty"}}}}}}}
	fmt.Fprintln(a.stdout, "# labels.json. A revision is a new version with supersedes and a reason; nothing is overwritten.")
	if err := writeJSON(a.stdout, l); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "# thresholds.json, stated before the evaluation and frozen with the labels:")
	return writeJSON(a.stdout, heldout.Thresholds{MinAnnotators: 2, MinItems: 30, MinPrecision: 0.8, MinRecall: 0.7, MinGroup: 10})
}

func appendHeld(ctx context.Context, a *app, kind, key string, v any) error {
	rs, err := a.records()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, _, err = rs.Append(ctx, store.RecordInput{Kind: kind, Key: key, Body: raw})
	return err
}

func loadCorpus(ctx context.Context, a *app) (heldout.Corpus, error) {
	rs, err := a.records()
	if err != nil {
		return heldout.Corpus{}, err
	}
	rec, err := rs.Latest(ctx, recHeldCorpus, "current")
	if errors.Is(err, store.ErrNoRecord) {
		return heldout.Corpus{}, errors.New("no corpus has been imported (heldout corpus import)")
	}
	if err != nil {
		return heldout.Corpus{}, err
	}
	var c heldout.Corpus
	err = json.Unmarshal(rec.Body, &c)
	return c, err
}

func heldoutCorpus(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 || args[0] != "import" {
		return usageErrorf("usage: heldout corpus import -file FILE")
	}
	fs := newFlags("heldout corpus import")
	file := fs.String("file", "", "corpus JSON (heldout example shows one)")
	if _, err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if *file == "" {
		return usageErrorf("usage: heldout corpus import -file FILE")
	}
	var c heldout.Corpus
	if err := readJSONFile(*file, &c); err != nil {
		return err
	}
	if err := validateCorpus(c); err != nil {
		return err
	}
	if err := appendHeld(ctx, a, recHeldCorpus, "current", c); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "corpus %q imported: %d items, %d labellers (%d attested independent).\nThe corpus is not evidence until independent labels exist, thresholds are stated, and all of it is frozen.\n", c.Name, len(c.Items), len(c.Labellers), countIndependent(c))
	return nil
}

func countIndependent(c heldout.Corpus) int {
	n := 0
	for _, l := range c.Labellers {
		if l.Independent {
			n++
		}
	}
	return n
}

func validateCorpus(c heldout.Corpus) error {
	if strings.TrimSpace(c.Name) == "" || strings.HasPrefix(c.Name, "REPLACE") {
		return errors.New("the corpus needs a name")
	}
	seen := map[string]bool{}
	for _, it := range c.Items {
		switch {
		case it.ID == "" || seen[it.ID]:
			return fmt.Errorf("item id %q is empty or repeated", it.ID)
		case it.SourceID == "" || it.URL == "":
			return fmt.Errorf("%s: an item is source-linked: it needs source_id and url", it.ID)
		case strings.Contains(it.SourceID+it.URL+it.ArticleID, "REPLACE"):
			return fmt.Errorf("%s: a REPLACE placeholder is still there", it.ID)
		case it.Licence != heldout.Redistributable && it.Licence != heldout.Internal:
			return fmt.Errorf("%s: licence must be redistributable or internal; an unknown licence cannot be used", it.ID)
		case it.Licence != heldout.Redistributable && (it.Title != "" || it.Summary != ""):
			return fmt.Errorf("%s: the text may be kept in the corpus only when the licence is redistributable; keep the hash and the article_id", it.ID)
		case it.Licence != heldout.Redistributable && it.ArticleID == "":
			return fmt.Errorf("%s: a text not kept here is read from the local store and needs article_id", it.ID)
		case len(it.Hash) != 64:
			return fmt.Errorf("%s: content_hash is the sha256 of the title, a newline and the summary", it.ID)
		case it.Licence == heldout.Redistributable && heldout.ContentHash(it.Title, it.Summary) != it.Hash:
			return fmt.Errorf("%s: the text does not match its content_hash", it.ID)
		}
		if _, err := time.Parse(time.DateOnly, it.Published[:min(10, len(it.Published))]); err != nil {
			return fmt.Errorf("%s: published %q is not a day or a time", it.ID, it.Published)
		}
		seen[it.ID] = true
	}
	ls := map[string]bool{}
	for _, l := range c.Labellers {
		if l.ID == "" || ls[l.ID] || strings.HasPrefix(l.ID, "REPLACE") {
			return fmt.Errorf("labeller id %q is empty, repeated or a placeholder", l.ID)
		}
		ls[l.ID] = true
	}
	return nil
}

func loadHeldLabels(ctx context.Context, a *app) ([]heldout.Record, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	recs, err := rs.All(ctx, recHeldLabel, "")
	if err != nil {
		return nil, err
	}
	var out []heldout.Record
	for _, r := range recs {
		var x heldout.Record
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return nil, fmt.Errorf("label %s does not decode: %w", r.Key, err)
		}
		out = append(out, x)
	}
	return out, nil
}

func heldoutLabel(ctx context.Context, a *app, args []string) error {
	fs := newFlags("heldout label")
	file := fs.String("file", "", "JSON list of label versions (heldout example)")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *file == "" {
		return usageErrorf("usage: heldout label -file FILE")
	}
	var recs []heldout.Record
	if err := readJSONFile(*file, &recs); err != nil {
		return err
	}
	c, err := loadCorpus(ctx, a)
	if err != nil {
		return err
	}
	if fr, err := loadFreeze(ctx, a); err != nil {
		return err
	} else if fr != "" {
		return errors.New("the labels are frozen: a change after the freeze would make the evaluation unrepeatable; start a new corpus version instead")
	}
	items, ls := map[string]bool{}, map[string]bool{}
	for _, it := range c.Items {
		items[it.ID] = true
	}
	for _, l := range c.Labellers {
		ls[l.ID] = true
	}
	for _, r := range recs {
		if !items[r.Item] || !ls[r.Labeller] {
			return fmt.Errorf("label of %q by %q: not an item or a labeller of the corpus", r.Item, r.Labeller)
		}
		if r.Label.Event != "" && !slices.ContainsFunc(c.Events, func(e heldout.EventDef) bool { return e.Key == r.Label.Event }) {
			return fmt.Errorf("label of %s by %s: event %q is not in the corpus's events", r.Item, r.Labeller, r.Label.Event)
		}
		for _, cl := range r.Label.Claims {
			for _, p := range cl.Parties {
				if p.Role != "subject" && p.Role != "counterparty" {
					return fmt.Errorf("label of %s by %s: party role %q must be subject or counterparty", r.Item, r.Labeller, p.Role)
				}
			}
		}
	}
	existing, err := loadHeldLabels(ctx, a)
	if err != nil {
		return err
	}
	if err := heldout.ValidateRecords(append(slices.Clone(existing), recs...)); err != nil {
		return err
	}
	for _, r := range recs {
		if err := appendHeld(ctx, a, recHeldLabel, fmt.Sprintf("%s|%s|%06d", r.Labeller, r.Item, r.Version), r); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.stdout, "%d label version(s) recorded. Earlier versions stay; the highest version of each is the one used.\n", len(recs))
	return nil
}

func loadAdjudications(ctx context.Context, a *app) ([]heldout.Adjudication, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	recs, err := rs.Current(ctx, recHeldAdjudicate, "")
	if err != nil {
		return nil, err
	}
	var out []heldout.Adjudication
	for _, r := range recs {
		var x heldout.Adjudication
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

func heldoutAdjudicate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("heldout adjudicate")
	item := fs.String("item", "", "the item")
	stage := fs.String("stage", "", "the stage: "+strings.Join(heldout.Stages, ", "))
	by := fs.String("by", "", "the independent person settling it (not one of the annotators of the item)")
	tokens := fs.String("tokens", "", "the settled value, comma separated, exactly as heldout disagreements prints values")
	why := fs.String("rationale", "", "why")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *item == "" || *stage == "" || *by == "" || strings.TrimSpace(*why) == "" {
		return usageErrorf("usage: heldout adjudicate -item ID -stage S -by PERSON -tokens a,b -rationale TEXT")
	}
	if !slices.Contains(heldout.Stages, *stage) {
		return usageErrorf("-stage %q is not one of %s", *stage, strings.Join(heldout.Stages, ", "))
	}
	if fr, err := loadFreeze(ctx, a); err != nil {
		return err
	} else if fr != "" {
		return errors.New("the labels are frozen: an adjudication after the freeze would change the evaluation")
	}
	var tk []string
	for _, t := range strings.Split(*tokens, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tk = append(tk, t)
		}
	}
	adj := heldout.Adjudication{Item: *item, Stage: *stage, By: *by, Tokens: tk, Rationale: *why}
	if err := appendHeld(ctx, a, recHeldAdjudicate, *item+"|"+*stage, adj); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "adjudication recorded. It counts only if the person is attested independent and did not label the item; heldout disagreements shows whether it was accepted.")
	return nil
}

func loadThresholds(ctx context.Context, a *app) (heldout.Thresholds, bool, error) {
	rs, err := a.records()
	if err != nil {
		return heldout.Thresholds{}, false, err
	}
	rec, err := rs.Latest(ctx, recHeldThresholds, "current")
	if errors.Is(err, store.ErrNoRecord) {
		return heldout.Thresholds{}, false, nil
	}
	if err != nil {
		return heldout.Thresholds{}, false, err
	}
	var t heldout.Thresholds
	err = json.Unmarshal(rec.Body, &t)
	return t, err == nil, err
}

func heldoutThresholds(ctx context.Context, a *app, args []string) error {
	fs := newFlags("heldout thresholds")
	file := fs.String("file", "", "JSON thresholds; without it the stored ones are shown")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *file == "" {
		t, ok, err := loadThresholds(ctx, a)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(a.stdout, "no thresholds have been stated: nothing can pass.")
			return nil
		}
		return writeJSON(a.stdout, t)
	}
	if fr, err := loadFreeze(ctx, a); err != nil {
		return err
	} else if fr != "" {
		return errors.New("the thresholds are frozen: they were stated before the evaluation so that they cannot follow the result")
	}
	var t heldout.Thresholds
	if err := readJSONFile(*file, &t); err != nil {
		return err
	}
	if err := t.Validate(); err != nil {
		return err
	}
	if err := appendHeld(ctx, a, recHeldThresholds, "current", t); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "thresholds recorded; they apply to the lower end of the 95% interval and freeze with the labels.")
	return nil
}

func loadFreeze(ctx context.Context, a *app) (string, error) {
	rs, err := a.records()
	if err != nil {
		return "", err
	}
	rec, err := rs.Latest(ctx, recHeldFreeze, "current")
	if errors.Is(err, store.ErrNoRecord) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var f struct {
		Hash string `json:"hash"`
	}
	err = json.Unmarshal(rec.Body, &f)
	return f.Hash, err
}

func heldoutFreeze(ctx context.Context, a *app) error {
	c, err := loadCorpus(ctx, a)
	if err != nil {
		return err
	}
	recs, err := loadHeldLabels(ctx, a)
	if err != nil {
		return err
	}
	adjs, err := loadAdjudications(ctx, a)
	if err != nil {
		return err
	}
	t, ok, err := loadThresholds(ctx, a)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("state the thresholds before freezing (heldout thresholds -file FILE)")
	}
	if err := t.Validate(); err != nil {
		return err
	}
	if prev, err := loadFreeze(ctx, a); err != nil {
		return err
	} else if prev != "" {
		return errors.New("already frozen: a frozen evaluation is not re-frozen; a corrected corpus is a new corpus")
	}
	h := heldout.FreezeHash(c, recs, adjs, t)
	if err := appendHeld(ctx, a, recHeldFreeze, "current", map[string]string{"hash": h, "at": a.now().UTC().Format(time.RFC3339)}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "frozen (%s): the corpus, %d label version(s), %d adjudication(s) and the thresholds can no longer change without the evaluation saying so.\n", h[:12], len(recs), len(adjs))
	return nil
}

func settled(ctx context.Context, a *app) (heldout.Corpus, heldout.Run, heldout.Gold, error) {
	c, err := loadCorpus(ctx, a)
	if err != nil {
		return c, heldout.Run{}, heldout.Gold{}, err
	}
	recs, err := loadHeldLabels(ctx, a)
	if err != nil {
		return c, heldout.Run{}, heldout.Gold{}, err
	}
	adjs, err := loadAdjudications(ctx, a)
	if err != nil {
		return c, heldout.Run{}, heldout.Gold{}, err
	}
	t, _, err := loadThresholds(ctx, a)
	if err != nil {
		return c, heldout.Run{}, heldout.Gold{}, err
	}
	fr, err := loadFreeze(ctx, a)
	if err != nil {
		return c, heldout.Run{}, heldout.Gold{}, err
	}
	run := heldout.Run{Corpus: c, Records: recs, Adjudicated: adjs, Thresholds: t, Frozen: fr}
	ann := t.MinAnnotators
	if ann < 2 {
		ann = 2
	}
	return c, run, heldout.Settle(c, recs, adjs, heldout.Thresholds{MinAnnotators: ann}), nil
}

func heldoutDisagreements(ctx context.Context, a *app, args []string) error {
	fs := newFlags("heldout disagreements")
	asJSON := fs.Bool("json", false, "print as JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	_, _, g, err := settled(ctx, a)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, g.Disagreements)
	}
	if len(g.Disagreements) == 0 {
		fmt.Fprintln(a.stdout, "no disagreement between independent annotators.")
		return nil
	}
	color := useColor(a.stdout)
	t := cliTable{Header: []string{"item", "stage", "state", "values by labeller", "settled by"}}
	for _, d := range g.Disagreements {
		var vals []string
		names := make([]string, 0, len(d.Values))
		for n := range d.Values {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			vals = append(vals, n+": "+strings.Join(d.Values[n], ", "))
		}
		state, st, by := "open", cliRed, ""
		switch {
		case d.Resolved:
			state, st, by = "settled", cliGreen, d.By+": "+strings.Join(d.Tokens, ", ")+" ("+d.Why+")"
		case d.Problem != "":
			state, st, by = "not accepted", cliYellow, d.Problem
		}
		t.Rows = append(t.Rows, []cliCell{styled(d.Item, cliStrong), plain(d.Stage), styled(state, st), plain(strings.Join(vals, " | ")), plain(by)})
	}
	t.write(a.stdout, color)
	return nil
}

func heldoutEvaluate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("heldout evaluate")
	asJSON := fs.Bool("json", false, "print as JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	c, run, _, err := settled(ctx, a)
	if err != nil {
		return err
	}
	var texts heldout.Texts
	if rd, rerr := a.reader(); rerr == nil {
		texts = func(it heldout.Item) (string, string, error) {
			art, err := rd.Get(ctx, it.ArticleID)
			if err != nil {
				return "", "", fmt.Errorf("article %s is not in this tenant's store: %w", it.ArticleID, err)
			}
			return art.Title, art.Summary, nil
		}
	}
	run.Predictions, run.Skipped = heldout.Predict(c, texts)
	rep := heldout.Evaluate(run)
	if *asJSON {
		return printJSON(a, rep)
	}
	printHeldout(a, rep)
	return nil
}

func gateStyle(g heldout.GateStatus) cliStyle {
	if g == heldout.Pass {
		return cliGreen
	}
	if g == heldout.Fail {
		return cliRed
	}
	return cliYellow
}

func printHeldout(a *app, r heldout.Report) {
	color := useColor(a.stdout)
	fmt.Fprintf(a.stdout, "%s  corpus %q, %d items, %d independently labelled; %d open and %d settled disagreements; %d superseded label versions\n",
		styledText("GATE "+strings.ToUpper(string(r.Gate)), gateStyle(r.Gate), color), r.Corpus, r.Items, r.Labelled, r.Open, r.Resolved, r.Superseded)
	for _, w := range r.GateWhy {
		fmt.Fprintln(a.stdout, "- "+w)
	}
	pct := func(i heldout.Interval) string {
		return fmt.Sprintf("%.3f [%.3f-%.3f]", i.Value, i.Low, i.High)
	}
	table := func(title string, ss []heldout.StageResult, gated bool) {
		fmt.Fprintln(a.stdout, title)
		hdr := []string{"stage", "items", "contested", "exact", "precision", "recall", "annotator agreement"}
		if gated {
			hdr = append(hdr, "gate", "why")
		}
		t := cliTable{Header: hdr}
		for _, s := range ss {
			row := []cliCell{styled(s.Stage, cliStrong), plain(fmt.Sprint(s.All.Items)), plain(fmt.Sprint(s.All.Contested)), plain(pct(s.All.ExactRate)), plain(pct(s.All.Precision)), plain(pct(s.All.Recall)), plain(pct(s.Agreement))}
			if gated {
				row = append(row, styled(string(s.Gate), gateStyle(s.Gate)), plain(s.Why))
			}
			t.Rows = append(t.Rows, row)
		}
		t.write(a.stdout, color)
	}
	table("independent evidence (counts toward the gate):", r.Independent, true)
	var grp []string
	for _, s := range r.Independent {
		for k, c := range s.Groups {
			grp = append(grp, fmt.Sprintf("%s/%s items %d, precision %s, recall %s", s.Stage, k, c.Items, pct(c.Precision), pct(c.Recall)))
		}
	}
	sort.Strings(grp)
	if len(grp) > 0 {
		fmt.Fprintln(a.stdout, "by group (a group with few items says little; intervals are 95%):")
		for _, g := range grp {
			fmt.Fprintln(a.stdout, "  "+g)
		}
	}
	table("synthetic regressions (NOT evidence, never count toward the gate):", r.Synthetic, false)
	if len(r.Skipped) > 0 {
		fmt.Fprintln(a.stdout, "left out of the figures:")
		var ids []string
		for id := range r.Skipped {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(a.stdout, "  %s: %s\n", id, r.Skipped[id])
		}
	}
}
