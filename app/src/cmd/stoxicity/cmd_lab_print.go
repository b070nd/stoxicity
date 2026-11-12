package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/research"
	"github.com/b070nd/stoxicity/app/src/store"
)

// printEventTypes lists every event type with its category and, per type, each field name, unit and meaning.
func printEventTypes(a *app) {
	t := cliTable{Header: []string{"type", "category", "field", "unit", "meaning"}}
	for _, ty := range eventfact.Types() {
		fields := ty.Fields()
		if len(fields) == 0 {
			t.Rows = append(t.Rows, []cliCell{styled(string(ty), cliStrong), plain(string(ty.Category())), plain(""), plain(""), plain("")})
			continue
		}
		for _, f := range fields {
			t.Rows = append(t.Rows, []cliCell{styled(string(ty), cliStrong), plain(string(ty.Category())), plain(f.Name), plain(f.Unit), plain(f.Meaning)})
		}
	}
	t.write(a.stdout, useColor(a.stdout))
}

// printFactCounts prints one row per type: type, records, how many are known (the rest are reconstructed), first and last availability day.
func printFactCounts(a *app, counts []store.EventFactCount) {
	if len(counts) == 0 {
		fmt.Fprintln(a.stdout, "no economic events are stored yet: run `stoxicity eventdb build`")
		return
	}
	t := cliTable{Header: []string{"type", "records", "known", "reconstructed", "first", "last"}, Right: []bool{false, true, true, true, false, false}}
	for _, c := range counts {
		t.Rows = append(t.Rows, []cliCell{styled(string(c.Type), cliStrong), plain(strconv.Itoa(c.Records)), plain(strconv.Itoa(c.Known)),
			plain(strconv.Itoa(c.Records - c.Known)), plain(labDay(c.First)), plain(labDay(c.Last))})
	}
	t.write(a.stdout, useColor(a.stdout))
}

// printEventFacts prints one row per record: available, symbol, type, source, basis and figures.
func printEventFacts(a *app, records []eventfact.Record) {
	if len(records) == 0 {
		fmt.Fprintln(a.stdout, "no economic events match")
		return
	}
	t := cliTable{Header: []string{"available", "symbol", "type", "source", "basis", "figures"}}
	for _, r := range records {
		t.Rows = append(t.Rows, []cliCell{plain(labWhen(r)), styled(r.Symbol, cliStrong), plain(string(r.Type)), plain(string(r.Source)), plain(string(r.Basis)), plain(labFigures(r))})
	}
	t.write(a.stdout, useColor(a.stdout))
}

// printEventFact prints everything about one record, then one line per revision.
func printEventFact(a *app, rec eventfact.Record, history []store.EventFactRevision) {
	w := a.stdout
	line := func(label, value string) { fmt.Fprintf(w, "%-12s%s\n", label+":", value) }

	line("id", rec.ID)
	line("key", rec.Key)
	line("type", string(rec.Type)+" ("+string(rec.Category)+")")
	sec := rec.Symbol
	switch {
	case rec.Symbol != "" && rec.SecurityID != "":
		sec = rec.Symbol + " (" + rec.SecurityID + ")"
	case rec.SecurityID != "":
		sec = rec.SecurityID
	}
	line("security", sec)
	line("published", labTime(rec.PublishedAt))
	line("available", labWhen(rec)+" (precision "+rec.Precision+")")
	line("source", string(rec.Source)+" "+rec.SourceRef)
	line("basis", string(rec.Basis))
	line("extractor", rec.Extractor+" "+rec.Version)
	if len(rec.Evidence) == 0 {
		line("evidence", "none")
	}
	for _, e := range rec.Evidence {
		line("evidence", e)
	}
	if len(rec.Facts) == 0 {
		line("facts", "none")
	}
	for i, f := range rec.Facts {
		label := ""
		if i == 0 {
			label = "facts"
		}
		text := f.Name + "=" + labFactValue(f)
		if f.Period != "" {
			text += " for " + f.Period
		}
		if f.Derived {
			text += " (derived)"
		}
		line(label, text)
		if f.Text != "" {
			line("", "read from \""+f.Text+"\"")
		}
	}
	for i, k := range labKeys(rec.Labels) {
		label := ""
		if i == 0 {
			label = "labels"
		}
		line(label, k+"="+rec.Labels[k])
	}
	if len(rec.Labels) == 0 {
		line("labels", "none")
	}
	fmt.Fprintln(w, "revisions:")
	for _, h := range history {
		fmt.Fprintf(w, "  revision %d, recorded %s, hash %s\n", h.Revision, labTime(h.RecordedAt), labShort(h.Hash))
	}
}

// printBuildResult prints what a build did: events read, insider transactions read, economic events derived, new revisions written, and one row per type.
func printBuildResult(a *app, r research.BuildResult) {
	fmt.Fprintf(a.stdout, "events read: %d\n", r.Events)
	fmt.Fprintf(a.stdout, "insider transactions read: %d\n", r.Trades)
	fmt.Fprintf(a.stdout, "economic events derived: %d (%d known, %d reconstructed)\n", r.Records, r.Known, r.Reconstructed)
	fmt.Fprintf(a.stdout, "new revisions written: %d\n", r.Written)
	t := cliTable{Header: []string{"type", "records"}, Right: []bool{false, true}}
	for _, k := range labKeys(r.ByType) {
		t.Rows = append(t.Rows, []cliCell{styled(k, cliStrong), plain(strconv.Itoa(r.ByType[k]))})
	}
	t.write(a.stdout, useColor(a.stdout))
}

// studyStyle is the colour of a study status: evaluated is green, experimental yellow, and a study with too little history dim.
func studyStyle(status string) cliStyle {
	switch status {
	case eventstudy.Evaluated:
		return cliGreen
	case eventstudy.Experimental:
		return cliYellow
	case eventstudy.Insufficient:
		return cliDim
	}
	return cliPlain
}

// printStudyReport prints a study report: its status, the counts by state, the prices it read, and tables of its groups and outcomes.
func printStudyReport(a *app, r eventstudy.Report, prices research.PriceResult, breakdown, outcomes bool) {
	color := useColor(a.stdout)
	fmt.Fprintf(a.stdout, "%s  study %s, significance corrected for %d validation group(s), spec %s, data %s\n",
		styledText(strings.ToUpper(r.Status), studyStyle(r.Status), color), r.Spec.ID, r.Tested, labShort(r.SpecHash), labShort(r.DataHash))
	for _, l := range r.Limits {
		fmt.Fprintln(a.stdout, "- "+l)
	}
	var counts []string
	for _, k := range labKeys(r.Counts) {
		counts = append(counts, k+" "+strconv.Itoa(r.Counts[k]))
	}
	if len(counts) == 0 {
		counts = []string{"none"}
	}
	fmt.Fprintln(a.stdout, "counts by state: "+strings.Join(counts, ", "))

	fmt.Fprintf(a.stdout, "prices: %d series read", prices.Series)
	if prices.First != "" {
		fmt.Fprintf(a.stdout, ", from %s to %s", prices.First, prices.Last)
	}
	fmt.Fprintln(a.stdout)
	// A security without usable bars is named by reason, not one line each: a
	// study over the whole market has hundreds, and -json lists every one.
	reasons := map[string][]string{}
	for _, tk := range labKeys(prices.Unresolved) {
		reasons[prices.Unresolved[tk]] = append(reasons[prices.Unresolved[tk]], tk)
	}
	for _, why := range labKeys(reasons) {
		fmt.Fprintf(a.stdout, "without usable bars (%s): %d securities\n", why, len(reasons[why]))
	}
	fmt.Fprintln(a.stdout)

	var all, split []eventstudy.Group
	for _, g := range r.Groups {
		if g.Dimension == "all" {
			all = append(all, g)
		} else {
			split = append(split, g)
		}
	}
	t := cliTable{
		Header: []string{"type", "sessions", "period", "events", "mean net", "median net", "win rate", "mean against market", "interval", "adjusted p", "mean drawdown", "max drawdown", "status", "why"},
		Right:  []bool{false, true, false, true, true, true, true, true, false, true, true, true, false, false},
	}
	for _, g := range all {
		p := g.Primary()
		vs := "n/a"
		if g.VsMarket != nil {
			vs = labPercent(g.VsMarket.Mean)
		}
		t.Rows = append(t.Rows, []cliCell{styled(g.Type, cliStrong), plain(strconv.Itoa(g.Sessions)), plain(g.Period), plain(strconv.Itoa(p.N)),
			plain(labPercent(g.Net.Mean)), plain(labPercent(g.Net.Median)), plain(fmt.Sprintf("%.1f%%", 100*g.Net.WinRate)), plain(vs),
			plain(labPercent(p.Lower) + " to " + labPercent(p.Upper)), plain(fmt.Sprintf("%.3f", p.PAdjusted)),
			plain(labPercent(g.MeanDrawdown)), plain(labPercent(g.MaxDrawdown)), styled(g.Status, studyStyle(g.Status)), plain(g.Why)})
	}
	t.write(a.stdout, color)

	if breakdown {
		fmt.Fprintln(a.stdout)
		b := cliTable{Header: []string{"type", "sessions", "period", "dimension", "bucket", "events", "mean of the primary measure", "interval", "status"},
			Right: []bool{false, true, false, false, false, true, true, false, false}}
		for _, g := range split {
			p := g.Primary()
			b.Rows = append(b.Rows, []cliCell{styled(g.Type, cliStrong), plain(strconv.Itoa(g.Sessions)), plain(g.Period), plain(g.Dimension), plain(g.Bucket),
				plain(strconv.Itoa(p.N)), plain(labPercent(p.Mean)), plain(labPercent(p.Lower) + " to " + labPercent(p.Upper)), styled(g.Status, studyStyle(g.Status))})
		}
		b.write(a.stdout, color)
	}

	if outcomes {
		fmt.Fprintln(a.stdout)
		o := cliTable{Header: []string{"observation", "security", "type", "state", "period", "entry day", "entry price", "held"},
			Right: []bool{false, false, false, false, false, false, true, false}}
		for _, x := range r.Outcomes {
			o.Rows = append(o.Rows, []cliCell{plain(x.Observation), styled(x.Security, cliStrong), plain(x.Type), plain(x.State), plain(x.Period),
				plain(x.EntryDay), plain(fmt.Sprintf("%.4f", x.EntryPrice)), plain(labHeld(x))})
		}
		o.write(a.stdout, color)
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "A study describes what happened after past events. It does not say what will happen.")
}

// printSignals prints one block per signal: its action, security, event type, rule and id, its explanation, the risk layer's answer and its tests.
func printSignals(a *app, signals []playbook.Signal, decisions map[string]playbook.Decision) {
	color := useColor(a.stdout)
	if len(signals) == 0 {
		fmt.Fprintln(a.stdout, "no rule of the playbook reads the events of this window")
	}
	for i, s := range signals {
		if i > 0 {
			fmt.Fprintln(a.stdout)
		}
		fmt.Fprintf(a.stdout, "%s  %s  %s  rule %s  signal %s\n",
			styledText(strings.ToUpper(string(s.Action)), labActionStyle(s.Action), color), s.Security, s.Type, s.Rule, s.ID)
		fmt.Fprintln(a.stdout, s.Explanation)
		d, ok := decisions[s.ID]
		switch {
		case !ok:
			fmt.Fprintln(a.stdout, "risk layer: not asked")
		case d.Approved:
			fmt.Fprintf(a.stdout, "risk layer: approved for %s by %s\n", d.Mode, d.By)
		default:
			fmt.Fprintf(a.stdout, "risk layer: not approved (%s)\n", strings.Join(d.Reasons, "; "))
		}
		t := cliTable{Header: []string{"name", "result", "detail"}}
		for _, x := range s.Tests {
			t.Rows = append(t.Rows, []cliCell{styled(x.Name, cliStrong), styled(string(x.Result), labResultStyle(x.Result)), plain(x.Detail)})
		}
		t.write(a.stdout, color)
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "A signal is a request to the risk layer. Nothing here places an order.")
}

// labPercent shows a return as a percentage with a sign and two decimals: 0.0123 is +1.23%.
func labPercent(v float64) string { return fmt.Sprintf("%+.2f%%", 100*v) }

// labDay is the day of t, in UTC.
func labDay(t time.Time) string { return t.UTC().Format("2006-01-02") }

// labTime is the time t to the minute, in UTC.
func labTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" }

// labWhen is when a record is available: a day when its precision is a day, otherwise the time.
func labWhen(rec eventfact.Record) string {
	if rec.Precision == eventfact.PrecisionDay {
		return labDay(rec.AvailableAt)
	}
	return labTime(rec.AvailableAt)
}

// labShort is the first 12 characters of a hash, or the whole of a shorter one.
func labShort(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// labKeys returns the keys of m, sorted.
func labKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// labFactValue is the value of a figure with its unit; a ratio is shown as a percentage.
func labFactValue(f eventfact.Fact) string {
	if f.Unit == "ratio" {
		return labPercent(f.Value)
	}
	// Whole numbers as they are, money and prices to at most four decimals.
	v := strconv.FormatFloat(f.Value, 'f', -1, 64)
	if f.Value != math.Trunc(f.Value) {
		v = strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f.Value, 'f', 4, 64), "0"), ".")
	}
	if f.Unit == "" {
		return v
	}
	return v + " " + f.Unit
}

// labFigures lists the figures of a record as name=value and its labels as name=word, joined with commas.
func labFigures(rec eventfact.Record) string {
	var parts []string
	for _, f := range rec.Facts {
		parts = append(parts, f.Name+"="+labFactValue(f))
	}
	for _, k := range labKeys(rec.Labels) {
		parts = append(parts, k+"="+rec.Labels[k])
	}
	return strings.Join(parts, ", ")
}

// labHeld lists the holding periods of an outcome: the return for each measured one, otherwise its state.
func labHeld(o eventstudy.Outcome) string {
	var parts []string
	for _, h := range o.Held {
		if h.State == eventstudy.Measured {
			parts = append(parts, fmt.Sprintf("%ds: %s", h.Sessions, labPercent(h.Net)))
		} else {
			parts = append(parts, fmt.Sprintf("%ds: %s", h.Sessions, h.State))
		}
	}
	return strings.Join(parts, ", ")
}

// labActionStyle is the colour of a signal's action: buy green, sell red, watch yellow.
func labActionStyle(a playbook.Action) cliStyle {
	switch a {
	case playbook.Buy:
		return cliGreen
	case playbook.Sell:
		return cliRed
	case playbook.Watch:
		return cliYellow
	}
	return cliPlain
}

// labResultStyle is the colour of a test's result: pass green, fail red, unknown yellow.
func labResultStyle(r playbook.Result) cliStyle {
	switch r {
	case playbook.Pass:
		return cliGreen
	case playbook.Fail:
		return cliRed
	case playbook.Unknown:
		return cliYellow
	}
	return cliPlain
}
