// Package research is the workflow that turns what the database already holds into the
// historical event database, runs studies over it and evaluates signal rules.
//
// It is the only place that knows both the store and the pure packages (eventfact, eventstudy,
// playbook). It reads the database through the small Store interface, takes the time as an
// argument and keeps no state.
package research

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/origin"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Store is what research needs of the database; *store.DB satisfies it.
type Store interface {
	SignalEvents(ctx context.Context, q store.SignalQuery) ([]store.SignalEvent, error)
	AttachOrigins(ctx context.Context, events []store.SignalEvent, asOf time.Time) error
	Range(ctx context.Context, from, to time.Time) ([]model.Article, error)
	Backfilled(ctx context.Context, ids []string) (map[string]store.Backfill, error)
	FilingMetas(ctx context.Context, accessions []string) (map[string]store.FilingMeta, error)
	InsiderTrades(ctx context.Context, since, cik string, limit int) ([]model.InsiderTrade, error)
	FactHistory(ctx context.Context, cik string) ([]fundamentals.Fact, error)
	RefTickers(ctx context.Context) ([]store.RefTicker, error)
	PutEventFacts(ctx context.Context, records []eventfact.Record, at time.Time) (int, error)
	EventFacts(ctx context.Context, q store.EventFactQuery) ([]eventfact.Record, error)
	CandleSeries(ctx context.Context, tickers []string, from, to string, known time.Time) (map[string][]marketdata.Candle, error)
	Splits(ctx context.Context) ([]marketdata.Split, error)
	SplitsAsOf(ctx context.Context, known time.Time) ([]marketdata.Split, error)
}

var _ Store = (*store.DB)(nil)

// lateFetch is how long after publication an article may have been fetched and still count as
// held when it was published.
const lateFetch = 72 * time.Hour

// BuildResult says what Build derived and stored.
type BuildResult struct {
	Events        int            // stored events read
	Trades        int            // insider transactions read
	Records       int            // economic events derived
	Written       int            // new revisions stored
	ByType        map[string]int // records per event type
	Known         int
	Reconstructed int
}

// Build derives the economic events of a window from what is stored and stores them, with now as
// the time of recording. Repeating it over unchanged data writes nothing.
func Build(ctx context.Context, s Store, from, to, now time.Time) (BuildResult, error) {
	res := BuildResult{ByType: map[string]int{}}
	events, err := s.SignalEvents(ctx, store.SignalQuery{From: from, To: to})
	if err != nil {
		return res, fmt.Errorf("research: events: %w", err)
	}
	if err := s.AttachOrigins(ctx, events, time.Time{}); err != nil {
		return res, fmt.Errorf("research: origins: %w", err)
	}
	res.Events = len(events)
	arts, err := s.Range(ctx, from, to)
	if err != nil {
		return res, fmt.Errorf("research: articles: %w", err)
	}
	articles := map[string]model.Article{}
	for _, a := range arts {
		articles[a.ID] = a
	}

	var ids, filings []string
	for _, e := range events {
		for _, x := range e.Evidence {
			ids = append(ids, x.ArticleID)
			if x.Form != "" {
				filings = append(filings, x.ArticleID)
			}
		}
	}
	trades, err := s.InsiderTrades(ctx, from.UTC().Format(time.DateOnly), "", 0)
	if err != nil {
		return res, fmt.Errorf("research: insider trades: %w", err)
	}
	trades = filedWithin(trades, from.UTC().Format(time.DateOnly), to.UTC().Format(time.DateOnly))
	res.Trades = len(trades)
	var accessions []string
	for _, t := range trades {
		accessions = append(accessions, t.Accession)
	}

	ids = uniqueSorted(append(ids, accessions...))
	backfills, err := s.Backfilled(ctx, ids)
	if err != nil {
		return res, fmt.Errorf("research: backfills: %w", err)
	}
	metas, err := s.FilingMetas(ctx, uniqueSorted(append(filings, accessions...)))
	if err != nil {
		return res, fmt.Errorf("research: filings: %w", err)
	}
	basis := func(id string) eventfact.Basis {
		if _, ok := backfills[id]; ok {
			return eventfact.Reconstructed
		}
		a, ok := articles[id]
		if !ok || a.Fetched.Sub(a.Published) > lateFetch {
			return eventfact.Reconstructed
		}
		return eventfact.Known
	}

	texts := make([]eventfact.EventText, 0, len(events))
	for _, e := range events {
		if t, ok := eventText(e, articles, backfills, metas, basis); ok {
			texts = append(texts, t)
		}
	}
	texts = oneSecurityPerIssuer(texts)
	var records []eventfact.Record
	for _, t := range texts {
		records = append(records, eventfact.FromEvent(t)...)
	}

	cache := map[string][]fundamentals.Fact{}
	var factErr error
	figures := func(cik, day string) (eventfact.Figures, bool) {
		facts, ok := cache[cik]
		if !ok {
			var ferr error
			facts, ferr = s.FactHistory(ctx, cik)
			if ferr != nil {
				if factErr == nil {
					factErr = fmt.Errorf("research: fundamentals of %s: %w", cik, ferr)
				}
				return eventfact.Figures{}, false
			}
			cache[cik] = facts
		}
		return figuresAsOf(facts, day)
	}
	records = append(records, eventfact.FromResults(texts, figures, eventfact.Surprise)...)
	if factErr != nil {
		return res, factErr
	}

	refs, err := s.RefTickers(ctx)
	if err != nil {
		return res, fmt.Errorf("research: tickers: %w", err)
	}
	times := map[string]eventfact.FilingTime{}
	for acc, m := range metas {
		times[acc] = eventfact.FilingTime{Accepted: m.Accepted, Precision: m.Precision}
	}
	records = append(records, eventfact.FromInsider(trades, securitiesOf(refs), times, basis)...)

	records, err = eventfact.Collapse(records)
	if err != nil {
		return res, fmt.Errorf("research: %w", err)
	}
	res.Records = len(records)
	for _, r := range records {
		res.ByType[string(r.Type)]++
		if r.Basis == eventfact.Known {
			res.Known++
		} else {
			res.Reconstructed++
		}
	}
	res.Written, err = s.PutEventFacts(ctx, records, now)
	if err != nil {
		return res, fmt.Errorf("research: storing events: %w", err)
	}
	return res, nil
}

// filedWithin keeps the trades whose form was filed on a day in [from, to].
func filedWithin(trades []model.InsiderTrade, from, to string) []model.InsiderTrade {
	var out []model.InsiderTrade
	for _, t := range trades {
		if t.Filed >= from && t.Filed <= to {
			out = append(out, t)
		}
	}
	return out
}

// securitiesOf maps the CIK of an issuer to its security; of several tickers the alphabetically
// first is taken so that the result is stable.
func securitiesOf(refs []store.RefTicker) map[string]eventfact.SecurityRef {
	out := map[string]eventfact.SecurityRef{}
	for _, r := range refs {
		if r.CIK == "" || r.Ticker == "" {
			continue
		}
		if cur, ok := out[r.CIK]; !ok || preferred(r.Ticker, cur.Symbol) {
			out[r.CIK] = eventfact.SecurityRef{Symbol: r.Ticker}
		}
	}
	return out
}

// preferred reports whether ticker a stands for an issuer before ticker b. An
// issuer's filing concerns the issuer, but its tickers are several securities:
// the common share, preferred series, notes, units. Counting the event once per
// ticker would count one economic event several times, so one ticker stands for
// the issuer: a plain symbol before one with a class or series suffix ("-", "."),
// then the shorter, then the alphabetically first. This is a rule of thumb read
// from the symbol alone; it is right for a common share next to its preferred
// series (JPM before JPM-PC) and arbitrary where the symbols do not tell.
func preferred(a, b string) bool {
	suffixed := func(t string) bool { return strings.ContainsAny(t, "-.") }
	switch {
	case suffixed(a) != suffixed(b):
		return !suffixed(a)
	case len(a) != len(b):
		return len(a) < len(b)
	}
	return a < b
}

// oneSecurityPerIssuer keeps, of the descriptions of one stored event for
// several securities of one issuer, the one whose ticker stands for the issuer
// (see preferred). Descriptions without an issuer number are kept as they are.
func oneSecurityPerIssuer(texts []eventfact.EventText) []eventfact.EventText {
	best := map[string]int{}
	for i, t := range texts {
		if t.CIK == "" {
			continue
		}
		k := t.EventID + "|" + t.CIK
		if j, ok := best[k]; !ok || preferred(t.Symbol, texts[j].Symbol) {
			best[k] = i
		}
	}
	out := make([]eventfact.EventText, 0, len(texts))
	for i, t := range texts {
		if t.CIK == "" || best[t.EventID+"|"+t.CIK] == i {
			out = append(out, t)
		}
	}
	return out
}

// eventText describes one stored event for the extractors; false when it has no evidence.
func eventText(e store.SignalEvent, articles map[string]model.Article, backfills map[string]store.Backfill,
	metas map[string]store.FilingMeta, basis func(string) eventfact.Basis) (eventfact.EventText, bool) {
	ev := append([]store.SignalEvidence(nil), e.Evidence...)
	sort.SliceStable(ev, func(i, j int) bool {
		if !ev[i].Published.Equal(ev[j].Published) {
			return ev[i].Published.Before(ev[j].Published)
		}
		return ev[i].ArticleID < ev[j].ArticleID
	})
	if len(ev) == 0 {
		return eventfact.EventText{}, false
	}
	lead := ev[0]

	var ids, items []string
	cik := ""
	var passages []eventfact.Passage
	seen := map[string]bool{}
	for _, x := range ev {
		if seen[x.ArticleID] {
			continue
		}
		seen[x.ArticleID] = true
		ids = append(ids, x.ArticleID)
		items = append(items, x.Items...)
		if cik == "" {
			cik = x.CIK
		}
		if x.Title != "" {
			passages = append(passages, eventfact.Passage{Ref: x.ArticleID, Text: x.Title})
		}
		if a, ok := articles[x.ArticleID]; ok && a.Summary != "" {
			passages = append(passages, eventfact.Passage{Ref: x.ArticleID, Text: a.Summary})
		}
	}
	sort.Strings(ids)

	filing := lead.Form != ""
	meta, hasMeta := metas[lead.ArticleID]
	bf, backfilled := backfills[lead.ArticleID]
	availableAt, precision := lead.Published, eventfact.PrecisionTime
	switch {
	case filing && hasMeta && meta.Precision == store.PrecisionTime && !meta.Accepted.IsZero():
		availableAt, precision = meta.Accepted.UTC(), eventfact.PrecisionTime
	case backfilled && (bf.Precision == store.PrecisionDay || (hasMeta && meta.Precision == store.PrecisionDay)):
		day := bf.FiledDay
		if day == "" {
			day = meta.FiledDay
		}
		if d, err := time.Parse(time.DateOnly, day); err == nil {
			availableAt = d
		} else {
			y, m, d := lead.Published.UTC().Date()
			availableAt = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		}
		precision = eventfact.PrecisionDay
	}
	if availableAt.Before(lead.Published) {
		availableAt = lead.Published
	}

	source := eventfact.SourceMedia
	switch {
	case filing:
		source = eventfact.SourceFiling
	case e.Origins[lead.ArticleID].Kind == origin.KindIssuer:
		source = eventfact.SourceCompany
	case e.Origins[lead.ArticleID].Kind == origin.KindWire:
		source = eventfact.SourceWire
	}
	return eventfact.EventText{
		EventID: e.EventID, SecurityID: e.SecurityID, Symbol: e.Ticker, CIK: cik,
		Kind:        model.EventKind(e.Kind),
		Items:       uniqueSorted(items),
		PublishedAt: lead.Published,
		AvailableAt: availableAt,
		Precision:   precision,
		Source:      source,
		SourceRef:   lead.ArticleID,
		Evidence:    ids,
		Basis:       basis(lead.ArticleID),
		Passages:    passages,
	}, true
}

// Observations turns records into what a study reads, ordered by availability and then ID.
// sectors gives the sector of a symbol and may be nil. The capitalisation is not known here and
// stays zero.
func Observations(records []eventfact.Record, sectors map[string]string) []eventstudy.Observation {
	out := make([]eventstudy.Observation, 0, len(records))
	for _, r := range records {
		o := eventstudy.Observation{
			ID: r.ID, Security: r.Symbol, Type: string(r.Type), AvailableAt: r.AvailableAt,
			DayPrecision:  r.Precision == eventfact.PrecisionDay,
			Reconstructed: r.Basis == eventfact.Reconstructed,
			// A rebuilt event is attested when an official filing with a recorded
			// acceptance time reports it: its availability is the regulator's fact.
			Attested: r.Basis == eventfact.Reconstructed && r.Source == eventfact.SourceFiling && r.Precision == eventfact.PrecisionTime,
			Sector:   sectors[r.Symbol],
			Labels:   make(map[string]string, len(r.Labels)+2),
		}
		if len(r.Facts) > 0 {
			o.Numbers = make(map[string]float64, len(r.Facts))
			for _, f := range r.Facts {
				o.Numbers[f.Name] = f.Value
			}
		}
		for k, v := range r.Labels {
			o.Labels[k] = v
		}
		o.Labels["source"], o.Labels["category"] = string(r.Source), string(r.Category)
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].AvailableAt.Equal(out[j].AvailableAt) {
			return out[i].AvailableAt.Before(out[j].AvailableAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// StudyInput is one run of a study over the stored events and bars.
type StudyInput struct {
	Spec     eventstudy.Spec
	Manifest eventstudy.Manifest
	Sectors  map[string]string
	AsOf     time.Time // read the events and bars as they were held then; zero: as they stand
}

// Study runs the study over the events and bars the database holds, as they were held at
// in.AsOf.
func Study(ctx context.Context, s Store, in StudyInput) (eventstudy.Report, PriceResult, error) {
	if err := in.Spec.Validate(); err != nil {
		return eventstudy.Report{}, PriceResult{}, err
	}
	q := store.EventFactQuery{AsOf: in.AsOf}
	for _, t := range in.Spec.Types {
		q.Types = append(q.Types, eventfact.Type(t))
	}
	q.From, q.To = studyWindow(in.Spec)
	records, err := s.EventFacts(ctx, q)
	if err != nil {
		return eventstudy.Report{}, PriceResult{}, fmt.Errorf("research: events: %w", err)
	}
	obs := Observations(records, in.Sectors)

	tickers := securitiesOfObservations(obs)
	tickers = append(tickers, in.Spec.Benchmark)
	sectorNames := make([]string, 0, len(in.Spec.SectorBenchmarks))
	for name := range in.Spec.SectorBenchmarks {
		sectorNames = append(sectorNames, name)
	}
	sort.Strings(sectorNames)
	for _, name := range sectorNames {
		tickers = append(tickers, in.Spec.SectorBenchmarks[name])
	}
	prices, pr := eventstudy.MapPrices{}, PriceResult{Unresolved: map[string]string{}}
	if len(obs) > 0 {
		first, last := obs[0].AvailableAt, obs[len(obs)-1].AvailableAt
		prices, pr, err = Prices(ctx, s, tickers, first.AddDate(0, 0, -400).UTC().Format(time.DateOnly),
			last.AddDate(0, 0, 60).UTC().Format(time.DateOnly), in.AsOf)
		if err != nil {
			return eventstudy.Report{}, pr, err
		}
	}
	report, err := eventstudy.Run(in.Spec, in.Manifest, obs, prices)
	return report, pr, err
}

// studyWindow is the span of availability the events are read for: the development and
// validation windows together, widened by 10 days at the start; open (zero times) when none is
// set.
func studyWindow(spec eventstudy.Spec) (from, to time.Time) {
	first, last := "", ""
	for _, w := range []eventstudy.Window{spec.Development, spec.Validation} {
		if w.From == "" && w.To == "" {
			continue
		}
		if first == "" || w.From < first {
			first = w.From
		}
		if w.To > last {
			last = w.To
		}
	}
	if d, err := time.Parse(time.DateOnly, first); err == nil {
		from = d.AddDate(0, 0, -10)
	}
	if d, err := time.Parse(time.DateOnly, last); err == nil {
		to = d.AddDate(0, 0, 1).Add(-time.Millisecond)
	}
	return from, to
}

func securitiesOfObservations(obs []eventstudy.Observation) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		out = append(out, o.Security)
	}
	return out
}

// SignalInput is one evaluation of a playbook on the events that became available in a window.
type SignalInput struct {
	Playbook playbook.Playbook
	Report   eventstudy.Report
	From, To time.Time
	Sectors  map[string]string
}

// Signals evaluates the playbook on every event that became available in [From, To], against
// the study report the playbook reads. The signals come ordered by availability, then event,
// then rule order.
func Signals(ctx context.Context, s Store, in SignalInput) ([]playbook.Signal, error) {
	if err := in.Playbook.Validate(); err != nil {
		return nil, err
	}
	records, err := s.EventFacts(ctx, store.EventFactQuery{From: in.From, To: in.To})
	if err != nil {
		return nil, fmt.Errorf("research: events: %w", err)
	}
	obs := Observations(records, in.Sectors)
	if len(obs) == 0 {
		return nil, nil
	}
	tickers := append(securitiesOfObservations(obs), in.Report.Spec.Benchmark)
	prices, _, err := Prices(ctx, s, tickers, in.From.AddDate(0, 0, -400).UTC().Format(time.DateOnly), in.To.UTC().Format(time.DateOnly), time.Time{})
	if err != nil {
		return nil, err
	}
	var out []playbook.Signal
	for _, o := range obs {
		ctxt, err := eventstudy.ContextFor(in.Report.Spec, o, prices)
		if err != nil {
			return nil, fmt.Errorf("research: conditions of %s: %w", o.ID, err)
		}
		sigs, err := playbook.Evaluate(in.Playbook, o, ctxt, in.Report)
		if err != nil {
			return nil, err
		}
		out = append(out, sigs...)
	}
	return out, nil
}
