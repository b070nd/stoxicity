package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/b070nd/stoxicity/app/src/dossier"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cmdSignals lists the securities whose events are corroborated, and says for
// each on what and whether one episode dominates. It is a screen for where to
// read first, not a trading signal.
func cmdSignals(ctx context.Context, a *app, args []string) error {
	fs := newFlags("signals")
	days := fs.Int("days", 7, "events made public in the last days")
	minMat := fs.Int("min", 2, "least materiality of an event that counts")
	limit := fs.Int("limit", 20, "most securities to show")
	all := fs.Bool("all", false, "also the ones that are not corroborated")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if err := checkWindow(*days, 1); err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	now := a.now()
	events, err := db.SignalEvents(ctx, now.AddDate(0, 0, -*days), now.Add(24*time.Hour), *minMat)
	if err != nil {
		return err
	}
	bySec := map[string][]store.SignalEvent{}
	for _, e := range events {
		bySec[e.SecurityID] = append(bySec[e.SecurityID], e)
	}
	type scored struct {
		ticker string
		v      dossier.Verdict
	}
	var list []scored
	for id, es := range bySec {
		v := dossier.Assess(es, a.isPrimary)
		if v.Corroborated || *all {
			list = append(list, scored{cmp.Or(es[0].Ticker, id), v})
		}
	}
	slices.SortFunc(list, func(x, y scored) int {
		return cmp.Or(cmp.Compare(y.v.Primary, x.v.Primary), cmp.Compare(y.v.Publishers, x.v.Publishers), cmp.Compare(y.v.Episodes, x.v.Episodes), cmp.Compare(x.ticker, y.ticker))
	})
	if len(list) == 0 {
		fmt.Fprintf(a.stdout, "nothing corroborated in the last %d days (%d securities with events of materiality %d or more); -all shows them\n", *days, len(bySec), *minMat)
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TICKER\tEPISODES\tBASIS\tPUBLISHERS\tPEAK\tVERDICT")
	for i, s := range list {
		if i == *limit {
			break
		}
		word := "not corroborated"
		if s.v.Corroborated {
			word = "corroborated"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%.0f%%\t%s: %s\n", s.ticker, s.v.Episodes, s.v.Basis, s.v.Publishers, s.v.PeakShare*100, word, s.v.Reason)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "\n%s. This measures the breadth of the evidence, not whether the news will move the price.\n", dossier.Caveat)
	return nil
}

// isPrimary says whether a source is a primary one (tier 1: a filing, a regulator).
func (a *app) isPrimary(sourceID string) bool { return a.reg[sourceID].Tier == model.TierPrimary }

// gatherSecurity reads what the dossier of a security needs.
func gatherSecurity(ctx context.Context, db *store.DB, sec store.SecurityInfo, since time.Time, limit int) ([]store.TimelineEntry, map[string][]model.EventRevision, map[string]model.Article, error) {
	timeline, err := db.SecurityTimeline(ctx, sec.Ticker, time.Time{}, limit)
	if err != nil {
		return nil, nil, nil, err
	}
	hist := map[string][]model.EventRevision{}
	arts := map[string]model.Article{}
	kept := timeline[:0]
	for _, t := range timeline {
		if t.PublicAt.Before(since) {
			continue
		}
		kept = append(kept, t)
		h, err := db.EventHistory(ctx, t.EventID)
		if err != nil {
			return nil, nil, nil, err
		}
		hist[t.EventID] = h
		for _, r := range h {
			for _, ev := range r.Evidence {
				if _, ok := arts[ev.ArticleID]; ok {
					continue
				}
				art, err := db.Get(ctx, ev.ArticleID)
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, nil, nil, err
				}
				arts[ev.ArticleID] = art
			}
		}
	}
	return kept, hist, arts, nil
}

// cmdGraph builds the small knowledge graph around one security: its events,
// the securities and topics they touch and the publishers of their evidence,
// with a verdict on whether the signal stands up or is a single peak.
func cmdGraph(ctx context.Context, a *app, args []string) error {
	fs := newFlags("graph")
	days := fs.Int("days", 30, "events made public in the last days")
	format := fs.String("format", "text", "text, json or dot")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("want one ticker")
	}
	if err := checkWindow(*days, 1); err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	sec, ok, err := db.SecurityInfoOf(ctx, rest[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no security with the ticker %s (run: stoxicity events rebuild)", strings.ToUpper(rest[0]))
	}
	timeline, hist, arts, err := gatherSecurity(ctx, db, sec, a.now().AddDate(0, 0, -*days), 200)
	if err != nil {
		return err
	}
	g, v := dossier.BuildGraph(sec, timeline, hist, arts, a.isPrimary)
	switch *format {
	case "json":
		fmt.Fprint(a.stdout, g.JSON())
	case "dot":
		fmt.Fprint(a.stdout, g.DOT())
	case "text":
		fmt.Fprint(a.stdout, g.Text())
		word := "not corroborated"
		if v.Corroborated {
			word = "corroborated"
		}
		fmt.Fprintf(a.stdout, "\nevidence: %s (%s): %s; peak share %.0f%%, %d sources\n%s.\n", word, v.Basis, v.Reason, v.PeakShare*100, v.Sources, v.Caveat)
	default:
		return usageErrorf("-format must be text, json or dot")
	}
	return nil
}

// securityFacts are the plain facts a dossier states about a security.
func (a *app) securityFacts(ctx context.Context, db *store.DB, sec store.SecurityInfo) []string {
	var out []string
	if latest, err := db.LatestCandles(ctx); err == nil {
		if c := latest[sec.Ticker]; len(c) > 0 {
			out = append(out, fmt.Sprintf("last close %.2f on %s", c[0].Close, c[0].Day))
			if len(c) > 1 {
				splits, _ := db.Splits(ctx)
				cov, _ := db.SplitCoverage(ctx)
				var mine []marketdata.Split
				for _, s := range splits {
					if marketdata.SameTicker(s.Ticker, sec.Ticker) {
						mine = append(mine, s)
					}
				}
				m := marketdata.Compare(c[1], c[0], mine, cov)
				switch {
				case m.Shown() && m.State == marketdata.MoveSplit:
					out = append(out, fmt.Sprintf("move %+.2f%% from %s, after %s", m.Change*100, c[1].Day, m.Note))
				case m.Shown():
					out = append(out, fmt.Sprintf("move %+.2f%% from %s", m.Change*100, c[1].Day))
				case m.State == marketdata.MoveUnresolved:
					out = append(out, "move from "+c[1].Day+" unresolved: "+m.Note)
				}
			}
		}
	}
	if sec.CIK != "" {
		if facts, err := db.Fundamentals(ctx); err == nil {
			if s, ok := fundamentals.Summarize(facts)[sec.CIK]; ok {
				for _, k := range []string{fundamentals.Revenue, fundamentals.NetIncome, fundamentals.Equity} {
					if v, ok := s.Num[k]; ok {
						out = append(out, fmt.Sprintf("%s %.0f for the fiscal year ended %s (SEC frames)", k, v, s.End))
					}
				}
			}
		}
		if trades, err := db.InsiderTrades(ctx, a.now().AddDate(0, 0, -insider.FigureDays).Format(time.DateOnly), sec.CIK, 0); err == nil {
			if s, ok := insider.Summarize(trades, a.now())[sec.CIK]; ok && (s.Buys > 0 || s.Sells > 0) {
				out = append(out, fmt.Sprintf("insiders, last %d days: bought %.0f (%d people), sold %.0f (%d people)", insider.FigureDays, s.Buys, s.Buyers, s.Sells, s.Sellers))
			}
		}
	}
	return out
}

// cmdExport writes the event history as Markdown files, one per event and one
// per security, that can be kept in Git: the database is the record, the files
// are the readable copy. A file is written only when its text changed, and none
// is ever deleted.
func cmdExport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("export")
	out := fs.String("out", "research", "folder to write to, with events/ and securities/ inside")
	days := fs.Int("days", 30, "events made public in the last days")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if err := checkWindow(*days, 1); err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	since := a.now().AddDate(0, 0, -*days)
	secs, err := db.SecuritiesWithEvents(ctx, since)
	if err != nil {
		return err
	}
	var wrote, same int
	write := func(path, text string) error {
		if old, err := os.ReadFile(path); err == nil && string(old) == text {
			same++
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
		wrote++
		return nil
	}
	doneEvents := map[string]bool{}
	for _, sec := range secs {
		if sec.Ticker == "" {
			continue
		}
		timeline, hist, arts, err := gatherSecurity(ctx, db, sec, since, 500)
		if err != nil {
			return err
		}
		if err := write(filepath.Join(*out, "securities", dossier.SecurityFile(sec.ID)),
			dossier.SecurityMarkdown(sec, a.securityFacts(ctx, db, sec), timeline)); err != nil {
			return err
		}
		for _, t := range timeline {
			if doneEvents[t.EventID] || len(hist[t.EventID]) == 0 {
				continue
			}
			doneEvents[t.EventID] = true
			if err := write(filepath.Join(*out, "events", dossier.EventFile(t.EventID)), dossier.EventMarkdown(hist[t.EventID], arts)); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(a.stdout, "exported %d securities and %d events to %s: %d files written, %d already up to date\n", len(secs), len(doneEvents), *out, wrote, same)
	return nil
}
