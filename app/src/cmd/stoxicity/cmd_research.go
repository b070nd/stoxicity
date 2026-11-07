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
	"time"

	"github.com/b070nd/stoxicity/app/src/dossier"
	"github.com/b070nd/stoxicity/app/src/episode"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/pipeline"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cmdSignals lists the securities whose recent events rest on evidence of a
// stated strength, and says for each on what. It is a screen for where to read
// first, not a trading signal.
//
// The strength is one of three states of the evidence, never a score:
//
//	primary       a filing or the issuer's own release reports it (primary
//	              evidence; quotes of it are not confirmation)
//	corroborated  at least two reporting origins that do not rest on each other
//	              report it (a primary document counts as one)
//	unresolved    independence is not established: one origin, no attribution,
//	              or only derivative articles
//
// Materiality takes no part in it. -min exists to read fewer events, and when it
// removes any the output says how many and that it can lower the states.
func cmdSignals(ctx context.Context, a *app, args []string) error {
	fs := newFlags("signals")
	days := fs.Int("days", 7, "events made public in the last days")
	minMat := fs.Int("min", 0, "read only events of at least this materiality (a keyword rule's reading); 0 reads every event. Filtering removes evidence before it is assessed and can lower the states")
	limit := fs.Int("limit", 20, "most securities to show")
	all := fs.Bool("all", false, "also the unresolved ones")
	at := fs.String("at", "", "judge what was known at this time (RFC 3339 or YYYY-MM-DD); nothing recorded or inferred later is used and nothing is stored")
	explain := fs.Bool("explain", false, "show the evidence behind each verdict")
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
	if *minMat < 0 || *minMat > 5 {
		return usageErrorf("-min must be between 0 and 5")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	now := a.now()
	var asOf time.Time
	if *at != "" {
		if asOf, err = parseWhen(*at); err != nil {
			return err
		}
		now = asOf
	}
	events, err := db.SignalEvents(ctx, store.SignalQuery{From: now.AddDate(0, 0, -*days), To: now.Add(24 * time.Hour), AsOf: asOf})
	if err != nil {
		return err
	}
	removed := 0
	if *minMat > 0 {
		kept := events[:0]
		for _, e := range events {
			if e.Materiality >= *minMat {
				kept = append(kept, e)
			} else {
				removed++
			}
		}
		events = kept
	}
	if asOf.IsZero() {
		if err := a.ensureOrigins(ctx, db, events); err != nil {
			return err
		}
	} else if err := db.AttachOrigins(ctx, events, asOf); err != nil {
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
	var fresh []store.EpisodeDecision
	for id, es := range bySec {
		decisions, stored, err := a.episodeDecisions(ctx, db, id, es, asOf)
		if err != nil {
			return err
		}
		fresh = append(fresh, stored...)
		v := dossier.Assess(es, decisions, a.isPrimary)
		if v.State != dossier.StateUnresolved || *all {
			list = append(list, scored{cmp.Or(es[0].Ticker, id), v})
		}
	}
	if asOf.IsZero() {
		if _, err := db.PutEpisodeDecisions(ctx, fresh, now); err != nil {
			return err
		}
	}
	slices.SortFunc(list, func(x, y scored) int {
		return cmp.Or(cmp.Compare(rankOf(y.v.State), rankOf(x.v.State)), cmp.Compare(y.v.CorroboratedEpisodes, x.v.CorroboratedEpisodes), cmp.Compare(y.v.Episodes, x.v.Episodes), cmp.Compare(x.ticker, y.ticker))
	})
	if *minMat > 0 {
		fmt.Fprintf(a.stdout, "materiality filter -min %d: %d events were left out before the evidence was assessed; the states below can be lower than with every event (-min 0)\n\n", *minMat, removed)
	}
	if len(list) == 0 {
		fmt.Fprintf(a.stdout, "no security with primary or corroborated evidence in the last %d days (%d securities with events); -all shows the unresolved ones\n", *days, len(bySec))
		return nil
	}
	t := cliTable{Header: []string{"TICKER", "STATE", "EPISODES", "ORIGINS", "SITES", "PEAK", "WHY"}, Right: []bool{false, false, true, true, true, true, false}}
	for i, s := range list {
		if i == *limit {
			break
		}
		independent := 0
		for _, d := range s.v.Details {
			independent += len(d.Independent)
		}
		t.Rows = append(t.Rows, []cliCell{styled(s.ticker, cliStrong), styled(string(s.v.State), stateLight(s.v.State)), plain(fmt.Sprint(s.v.Episodes)), plain(fmt.Sprint(independent)),
			plain(fmt.Sprint(s.v.Publishers)), styled(fmt.Sprintf("%.0f%%", s.v.PeakShare*100), peakLight(s.v)), plain(s.v.Reason)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintf(a.stdout, "\nstates: primary = a filing or the issuer's own release; corroborated = two or more origins that do not rest on each other; unresolved = independence not established. ORIGINS counts independent reporting origins (copies once), SITES the publishers. PEAK is the share of reports (distinct origins, copies once, counted per episode) in the largest episode over the last %d days.\n%s.\n", *days, dossier.Caveat)
	if *explain {
		for i, s := range list {
			if i == *limit {
				break
			}
			fmt.Fprintf(a.stdout, "\n%s\n", s.ticker)
			for n, d := range s.v.Details {
				fmt.Fprintf(a.stdout, "  episode %d (%s, from %s): %s: %s\n", n+1, d.Episode.Kind, d.Episode.Start.UTC().Format("2006-01-02 15:04"), d.State, d.Reason)
				for _, b := range d.Episode.Basis {
					fmt.Fprintf(a.stdout, "    joined: %s\n", b)
				}
				for _, l := range d.Lines {
					fmt.Fprintf(a.stdout, "    %s\n", l)
				}
			}
		}
	}
	return nil
}

func rankOf(s dossier.State) int {
	switch s {
	case dossier.StateCorroborated:
		return 3
	case dossier.StatePrimary:
		return 2
	case dossier.StateUnresolved:
		return 1
	}
	return 0
}

// stateLight: corroborated is green, primary evidence alone yellow, unresolved red.
func stateLight(s dossier.State) cliStyle {
	switch s {
	case dossier.StateCorroborated:
		return cliGreen
	case dossier.StatePrimary:
		return cliYellow
	}
	return cliRed
}

func peakLight(v dossier.Verdict) cliStyle {
	if v.SinglePeak && v.Episodes > 1 {
		return cliRed
	}
	if v.Episodes <= 1 {
		return cliDim
	}
	return cliGreen
}

// ensureOrigins assesses and stores the origin of every evidence article that
// has none, reading each such event's whole evidence so that copies are joined
// to the report they copy, and puts the origins on the events.
func (a *app) ensureOrigins(ctx context.Context, db *store.DB, events []store.SignalEvent) error {
	asOf := time.Time{}
	if err := db.AttachOrigins(ctx, events, asOf); err != nil {
		return err
	}
	var arts []model.Article
	seen := map[string]bool{}
	for _, e := range events {
		missing := false
		for _, x := range e.Evidence {
			if _, ok := e.Origins[x.ArticleID]; !ok {
				missing = true
			}
		}
		if !missing {
			continue
		}
		for _, x := range e.Evidence {
			if seen[x.ArticleID] {
				continue
			}
			seen[x.ArticleID] = true
			art, err := db.Get(ctx, x.ArticleID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			art.ClusterID = strings.TrimPrefix(e.EventID, "evt_") // group by the event the evidence is in
			arts = append(arts, art)
		}
	}
	if len(arts) == 0 {
		return nil
	}
	if _, err := db.PutOrigins(ctx, pipeline.Origins(arts, a.isPrimary), a.now()); err != nil {
		return err
	}
	return db.AttachOrigins(ctx, events, asOf)
}

// episodeDecisions decides which events of one security are one episode. At the
// present it computes the decisions from the evidence, returns them for storing
// and uses them. As of an earlier time it uses only the decisions that were
// stored by then: a relationship found later is not read back into the past, so
// a pair without a stored decision is undecided.
func (a *app) episodeDecisions(ctx context.Context, db *store.DB, securityID string, events []store.SignalEvent, asOf time.Time) ([]episode.Decision, []store.EpisodeDecision, error) {
	if asOf.IsZero() {
		ds := episode.Decisions(dossier.SignalEpisodeEvents(events))
		var rows []store.EpisodeDecision
		for _, d := range ds {
			rows = append(rows, store.EpisodeDecision{SecurityID: securityID, EventA: d.A, EventB: d.B, Decision: string(d.Verdict), Basis: d.Basis, Method: d.Method, Confidence: d.Confidence, Version: episode.Version})
		}
		return ds, rows, nil
	}
	stored, err := db.EpisodeDecisions(ctx, securityID, asOf)
	if err != nil {
		return nil, nil, err
	}
	var ds []episode.Decision
	for k, d := range stored {
		ds = append(ds, episode.Decision{A: k[0], B: k[1], Verdict: episode.Verdict(d.Decision), Basis: d.Basis, Method: d.Method, Confidence: d.Confidence, Probable: d.Method == episode.MethodFilingRelease || d.Method == episode.MethodStatementRef})
	}
	return ds, nil, nil
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
	stored, err := db.Origins(ctx, articleIDs(arts), time.Time{})
	if err != nil {
		return err
	}
	g, v := dossier.BuildGraph(sec, timeline, hist, arts, stored, a.isPrimary)
	switch *format {
	case "json":
		fmt.Fprint(a.stdout, g.JSON())
	case "dot":
		fmt.Fprint(a.stdout, g.DOT())
	case "text":
		fmt.Fprint(a.stdout, g.Text())
		fmt.Fprintf(a.stdout, "\nevidence: %s: %s; %d episodes, peak %.0f%% (%d of %d reports, copies once), %d sources\n%s.\n", v.State, v.Reason, v.Episodes, v.PeakShare*100, v.PeakReports, v.TotalReports, v.Sources, v.Caveat)
		for n, d := range v.Details {
			fmt.Fprintf(a.stdout, "  episode %d (%s): %s: %s\n", n+1, d.Episode.Kind, d.State, d.Reason)
		}
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

func articleIDs(arts map[string]model.Article) []string {
	ids := make([]string, 0, len(arts))
	for id := range arts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
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
		origins, err := db.Origins(ctx, articleIDs(arts), time.Time{})
		if err != nil {
			return err
		}
		facts := a.securityFacts(ctx, db, sec)
		if _, v := dossier.BuildGraph(sec, timeline, hist, arts, origins, a.isPrimary); v.Events > 0 {
			facts = append(facts, fmt.Sprintf("evidence in this window: %s (%s); %d episodes, %d sites. Judged from reporting origins, never from materiality. %s", v.State, v.Reason, v.Episodes, v.Publishers, dossier.Caveat))
		}
		if err := write(filepath.Join(*out, "securities", dossier.SecurityFile(sec.ID)),
			dossier.SecurityMarkdown(sec, facts, timeline)); err != nil {
			return err
		}
		for _, t := range timeline {
			if doneEvents[t.EventID] || len(hist[t.EventID]) == 0 {
				continue
			}
			doneEvents[t.EventID] = true
			if err := write(filepath.Join(*out, "events", dossier.EventFile(t.EventID)), dossier.EventMarkdown(hist[t.EventID], arts, origins)); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(a.stdout, "exported %d securities and %d events to %s: %d files written, %d already up to date\n", len(secs), len(doneEvents), *out, wrote, same)
	return nil
}
