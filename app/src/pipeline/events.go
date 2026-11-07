package pipeline

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/origin"
	"github.com/b070nd/stoxicity/app/src/store"
)

// minOtherMateriality is the materiality at which an article of no known kind
// is still an event worth keeping.
const minOtherMateriality = 3

// EventID is the identity of the event an article is evidence of: the story
// (cluster) it belongs to, or the article alone when it has none.
func EventID(a model.Article) string {
	if a.ClusterID != "" {
		return "evt_" + a.ClusterID
	}
	return "evt_" + a.ID
}

// Events reads the events out of articles that are scored and clustered. An
// article that says nothing of an event (no kind, little weight) gives none;
// articles of one story give one, with the securities they name (the strongest
// role of each), all of them as evidence, and the earliest publication time.
func Events(articles []model.Article) []store.EventInput { return EventsFrom(articles, nil, nil) }

// Claims reads what an article claims: one assertion for each event it reports,
// each with the companies it is about and the sentence it comes from.
func Claims(a model.Article, names func(ticker string) []string, rating func(items []string) (model.ItemRating, bool)) assertion.Result {
	return assertion.Extract(assertion.Input{
		ArticleID: a.ID, Title: a.Title, Summary: a.Summary, Form: a.Form, Items: a.Items, Tickers: a.Tickers,
		Published: a.Published, Score: a.Score, Names: names, ItemRating: rating,
	})
}

// EventsFrom reads the events out of articles whose claims have been read.
//
// A story can be several events, and an article several claims. Each live claim
// about a company (not negated, denied, past, a preview, or about nobody) makes
// evidence of an event of its kind in its story; the claims of one kind in a
// story are one event, with only the companies those claims name and only the
// articles that make them. An article that announces earnings, a departure and a
// deal is evidence of three events, and a company mentioned in one sentence has
// no part in the others.
//
// Identity: the story's first event keeps the story's own ID ("evt_<story>"), so
// every event that existed before keeps its ID and its meaning; homes says which
// kind that is for stories already stored. A story seen for the first time gives
// that ID to its most important claim. The other events of the story are
// "evt_<story>~<kind>" and say, in their relations, that they split from the
// first. An article without claims in the map is read as Events always did, from
// the lens's score, as one event of its story.
func EventsFrom(articles []model.Article, claims map[string]assertion.Result, homes map[string]model.EventKind) []store.EventInput {
	type cand struct {
		art     model.Article
		kind    model.EventKind
		claims  []assertion.Assertion
		mat     int
		top     assertion.Assertion
		total   int
		version string
	}
	var cands []cand
	var legacy []model.Article
	for _, a := range articles {
		res, have := claims[a.ID]
		if !have {
			if isEvent(a) {
				legacy = append(legacy, a)
			}
			continue
		}
		byKind := map[model.EventKind]*cand{}
		var order []model.EventKind
		for _, x := range res.Assertions {
			if !x.Live() || !hasActor(x) {
				continue
			}
			m, _ := x.Materiality()
			if x.Kind == model.EventOther && m < minOtherMateriality {
				continue
			}
			c := byKind[x.Kind]
			if c == nil {
				c = &cand{art: a, kind: x.Kind, total: len(res.Assertions), version: res.Version}
				byKind[x.Kind] = c
				order = append(order, x.Kind)
			}
			c.claims = append(c.claims, x)
			if len(c.claims) == 1 || m > c.mat || (m == c.mat && x.Seq < c.top.Seq) {
				c.mat, c.top = m, x
			}
		}
		// Chatter about a price or a rating is an event only when it is all the
		// article says: beside a result or a deal it is background.
		substantive := slices.ContainsFunc(order, func(k model.EventKind) bool { return !lens.Noise(k) })
		for _, k := range order {
			if substantive && lens.Noise(k) {
				continue
			}
			cands = append(cands, *byKind[k])
		}
	}

	// Which kind of a story keeps the story's ID.
	best := map[string]struct {
		kind model.EventKind
		mat  int
	}{}
	for _, c := range cands {
		base := EventID(c.art)
		b, ok := best[base]
		if !ok || c.mat > b.mat || (c.mat == b.mat && assertion.Rank(c.kind) < assertion.Rank(b.kind)) {
			best[base] = struct {
				kind model.EventKind
				mat  int
			}{c.kind, c.mat}
		}
	}
	idOf := func(base string, kind model.EventKind) string {
		home, ok := homes[base]
		if !ok {
			home = best[base].kind
		}
		if kind == home {
			return base
		}
		return base + "~" + string(kind)
	}
	var out []store.EventInput
	at := map[string]int{}
	for _, c := range cands {
		a := c.art
		base := EventID(a)
		id := idOf(base, c.kind)
		i, seen := at[id]
		rel, why := eventScore(a, c.kind, c.mat, c.top, c.total)
		if !seen {
			at[id] = len(out)
			out = append(out, store.EventInput{
				EventID: id, Kind: c.kind, Title: a.Title, PublicAt: a.Published,
				Materiality: c.mat, Relevance: rel, LensVersion: lensVersion(a.Score.LensVersion, c.version), Rationale: why,
			})
			i = len(out) - 1
			if id != base {
				out[i].Relations = append(out[i].Relations, model.EventRelation{From: id, To: base, Relation: model.RelationSplitFrom,
					Basis: "another event of the same story, read from a different claim of its articles"})
			}
		}
		e := &out[i]
		if a.Published.Before(e.PublicAt) {
			e.PublicAt = a.Published
		}
		named := false
		if seen && c.mat > e.Materiality { // the most material claim names the event
			e.Title, e.Materiality, e.Relevance, e.LensVersion, e.Rationale = a.Title, c.mat, rel, lensVersion(a.Score.LensVersion, c.version), why
			named = true
		}
		if !seen {
			named = true
		}
		if named {
			for k := range e.Claims {
				e.Claims[k].Names = false
			}
		}
		e.Evidence = append(e.Evidence, model.Evidence{ArticleID: a.ID, SourceID: a.SourceID})
		for _, x := range c.claims {
			e.Claims = append(e.Claims, model.EventClaim{ArticleID: a.ID, Seq: x.Seq, Version: c.version, Extractor: assertion.ExtractorRules, Names: named && x.Seq == c.top.Seq})
			for _, p := range x.Parties {
				r := model.RoleMentioned
				switch p.Role {
				case assertion.Subject:
					r = model.RoleSubject
				case assertion.Counterparty:
					r = model.RoleCounterparty
				}
				if k := slices.IndexFunc(e.Securities, func(s model.EventSecurity) bool { return s.Ticker == p.Ticker }); k >= 0 {
					if r.Strength() > e.Securities[k].Role.Strength() {
						e.Securities[k].Role = r
					}
				} else {
					e.Securities = append(e.Securities, model.EventSecurity{Ticker: p.Ticker, Role: r})
				}
			}
		}
		for _, t := range a.Topics {
			if !slices.Contains(e.Topics, t) {
				e.Topics = append(e.Topics, t)
			}
		}
	}

	// Articles without claims: one event of the story, as the lens read it.
	for _, a := range legacy {
		id := EventID(a)
		i, seen := at[id]
		if !seen {
			at[id] = len(out)
			out = append(out, store.EventInput{
				EventID: id, Kind: a.Score.Kind, Title: a.Title, PublicAt: a.Published,
				Materiality: a.Score.Materiality, Relevance: a.Score.Relevance, LensVersion: a.Score.LensVersion,
				Rationale: strings.Join(a.Score.Reasons, "; "),
			})
			i = len(out) - 1
		}
		e := &out[i]
		if a.Published.Before(e.PublicAt) {
			e.PublicAt = a.Published
		}
		if seen && a.Score.Materiality > e.Materiality {
			e.Kind, e.Title, e.Materiality, e.Relevance, e.LensVersion = a.Score.Kind, a.Title, a.Score.Materiality, a.Score.Relevance, a.Score.LensVersion
			e.Rationale = strings.Join(a.Score.Reasons, "; ")
		}
		e.Evidence = append(e.Evidence, model.Evidence{ArticleID: a.ID, SourceID: a.SourceID})
		for _, tk := range a.Tickers {
			e.Securities = append(e.Securities, model.EventSecurity{Ticker: tk, Role: RoleOf(a, tk)})
		}
		for _, t := range a.Topics {
			if !slices.Contains(e.Topics, t) {
				e.Topics = append(e.Topics, t)
			}
		}
	}
	return out
}

// lensVersion marks a score as read from claims: the version of the rules that
// read them follows the lens's own.
func lensVersion(lens, claims string) string {
	if lens == "" || claims == "" || strings.Contains(lens, "+"+claims) {
		return lens
	}
	return lens + "+" + claims
}

// eventScore is the relevance of the article's evidence for one event, and the
// words that explain which claim made it that event. The lens rated the article
// by its first keyword; the claim moves the relevance by the difference in
// materiality, at the lens's own 0.15 a level.
func eventScore(a model.Article, kind model.EventKind, mat int, top assertion.Assertion, total int) (float64, string) {
	rel := a.Score.Relevance
	if mat != a.Score.Materiality {
		rel = math.Min(math.Max(rel+0.15*float64(mat-a.Score.Materiality), 0), 1)
	}
	if lens.Noise(kind) && rel > 0.35 {
		rel = 0.35
	}
	rel = math.Round(rel*1000) / 1000
	_, mwhy := top.Materiality()
	why := fmt.Sprintf("claim %d of %d in the article: %s (%q); %s", top.Seq, total, kind, top.Match, mwhy)
	if top.Status != assertion.Unknown && top.Status != "" {
		why += "; status " + string(top.Status)
	}
	if top.Attribution != assertion.Unattributed {
		why += "; attribution " + string(top.Attribution)
	}
	if top.Action != "" {
		why += "; action " + top.Action
	}
	return rel, why
}

func hasActor(a assertion.Assertion) bool {
	return slices.ContainsFunc(a.Parties, func(p assertion.Party) bool { return p.Role != assertion.Mentioned })
}

func isEvent(a model.Article) bool {
	k := a.Score.Kind
	if k == "" || a.Score.LensVersion == "" {
		return false
	}
	return k != model.EventOther || a.Score.Materiality >= minOtherMateriality
}

// RoleOf says how the article relates the ticker to its event, from what the
// evidence shows: a filing is about its filer; an article that names one company
// is about it; one that names several is about those in its title and merely
// mentions the rest (a footer, a roundup, a comparison).
func RoleOf(a model.Article, ticker string) model.Role {
	if a.CIK != "" || len(a.Tickers) <= 1 {
		return model.RoleSubject
	}
	if inTitle(a.Title, ticker) {
		return model.RoleSubject
	}
	return model.RoleMentioned
}

func inTitle(title, ticker string) bool {
	ticker = strings.ToUpper(ticker)
	for _, w := range strings.FieldsFunc(strings.ToUpper(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-'
	}) {
		if strings.Trim(w, ".-") == strings.Trim(ticker, ".-") {
			return true
		}
	}
	return false
}

// recordEvents writes the events of an ingested batch. The evidence is stored
// already; a failure here is logged and repaired by rebuilding the events from
// the articles, it never loses news.
func (p *Pipeline) recordEvents(ctx context.Context, batch []model.Article) {
	ev, ok := p.cfg.Store.(store.Events)
	if !ok {
		return
	}
	claims := p.claims(batch)
	in := EventsFrom(batch, claims, p.homes(ctx, batch))
	if len(in) == 0 {
		p.recordClaims(ctx, batch, claims, nil)
		return
	}
	prior := p.priorEvidence(ctx, in)
	if _, err := ev.PutEvents(ctx, in, p.cfg.Now()); err != nil {
		p.cfg.Logger.Warn("pipeline: record events failed; rebuild them with: stoxicity events rebuild", "err", err)
		return
	}
	p.recordClaims(ctx, batch, claims, prior)
	p.recordOrigins(ctx, batch, in)
}

// homeStore is what finding the stories' own events needs of the database.
type homeStore interface {
	HomeKinds(ctx context.Context, eventIDs []string) (map[string]model.EventKind, error)
}

// homes says, for each story of the batch that already has an event under its
// own ID, which kind that event is, so that the ID keeps its meaning.
func (p *Pipeline) homes(ctx context.Context, batch []model.Article) map[string]model.EventKind {
	st, ok := p.cfg.Store.(homeStore)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, a := range batch {
		if id := EventID(a); !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	h, err := st.HomeKinds(ctx, ids)
	if err != nil {
		p.cfg.Logger.Warn("pipeline: read the stored events failed; their identity may be read again", "err", err)
		return nil
	}
	return h
}

// Homes reads, for the stories of the articles, the kind of the event each
// already has under its own ID.
func Homes(ctx context.Context, st homeStore, articles []model.Article) (map[string]model.EventKind, error) {
	seen := map[string]bool{}
	var ids []string
	for _, a := range articles {
		if id := EventID(a); !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return st.HomeKinds(ctx, ids)
}

// names gives the names a ticker is written by, from the entity list.
func (p *Pipeline) names(ticker string) []string {
	if p.cfg.Entities == nil {
		return nil
	}
	e, ok := p.cfg.Entities.Lookup(ticker)
	if !ok {
		return nil
	}
	return append([]string{e.Name}, e.Aliases...)
}

// claims reads the claims of each article of the batch.
func (p *Pipeline) claims(batch []model.Article) map[string]assertion.Result {
	out := make(map[string]assertion.Result, len(batch))
	for _, a := range batch {
		out[a.ID] = Claims(a, p.names, p.cfg.ItemRating)
	}
	return out
}

// priorEvidence is how many pieces of evidence each event of the batch had
// before the batch, from its latest stored revision.
func (p *Pipeline) priorEvidence(ctx context.Context, in []store.EventInput) map[string]int {
	st, ok := p.cfg.Store.(originStore)
	if !ok {
		return nil
	}
	out := map[string]int{}
	for _, e := range in {
		if hist, err := st.EventHistory(ctx, e.EventID); err == nil && len(hist) > 0 {
			out[e.EventID] = len(hist[len(hist)-1].Evidence)
		}
	}
	return out
}

// claimStore is what recording claims needs of the database.
type claimStore interface {
	PutAssertions(ctx context.Context, results []assertion.Result, at time.Time) (int, error)
	PutAssessments(ctx context.Context, as []assertion.Assessment, at time.Time) (int, error)
}

// recordClaims stores the claims of the batch and what they are read to mean.
// Both are appended; a failure is logged and repaired by `events rebuild`.
func (p *Pipeline) recordClaims(ctx context.Context, batch []model.Article, claims map[string]assertion.Result, prior map[string]int) {
	st, ok := p.cfg.Store.(claimStore)
	if !ok {
		return
	}
	results, assessments := Assessments(batch, claims, prior, p.cfg.Now())
	if len(results) == 0 {
		return
	}
	at := p.cfg.Now()
	if _, err := st.PutAssertions(ctx, results, at); err != nil {
		p.cfg.Logger.Warn("pipeline: record assertions failed; repair them with: stoxicity events rebuild", "err", err)
		return
	}
	if _, err := st.PutAssessments(ctx, assessments, at); err != nil {
		p.cfg.Logger.Warn("pipeline: record assessments failed; repair them with: stoxicity events rebuild", "err", err)
	}
}

// Assessments reads the claims of the articles that make any into assessments.
// prior is how many pieces of evidence the event of an article had before the
// batch (nil: none); an article is also preceded by those of its event in the
// batch that were published before it.
func Assessments(batch []model.Article, claims map[string]assertion.Result, prior map[string]int, now time.Time) ([]assertion.Result, []assertion.Assessment) {
	byEvent := map[string][]model.Article{}
	for _, a := range batch {
		byEvent[EventID(a)] = append(byEvent[EventID(a)], a)
	}
	var results []assertion.Result
	var as []assertion.Assessment
	for _, a := range batch {
		r, ok := claims[a.ID]
		if !ok || len(r.Assertions) == 0 {
			continue
		}
		earlier := prior[EventID(a)]
		for _, o := range byEvent[EventID(a)] {
			if o.ID != a.ID && (o.Published.Before(a.Published) || (o.Published.Equal(a.Published) && o.ID < a.ID)) {
				earlier++
			}
		}
		results = append(results, r)
		as = append(as, assertion.Assess(r, assertion.Context{Earlier: earlier, Now: now, Published: a.Published})...)
	}
	return results, as
}

// originStore is what recording origins needs of the database.
type originStore interface {
	PutOrigins(ctx context.Context, os []origin.Origin, at time.Time) (int, error)
	EventHistory(ctx context.Context, eventID string) ([]model.EventRevision, error)
	Get(ctx context.Context, id string) (model.Article, error)
}

// OriginInput is what origin detection reads of an article.
func OriginInput(a model.Article, primary func(sourceID string) bool) origin.Input {
	return origin.Input{ArticleID: a.ID, SourceID: a.SourceID, Domain: a.Domain, URL: a.URL, Title: a.Title, Summary: a.Summary,
		Form: a.Form, Primary: primary != nil && primary(a.SourceID), Published: a.Published}
}

// Origins finds the reporting origin of each article: each by its own text, then
// the copies among the articles of one event joined to the first of them. The
// order of the result is that of the input.
func Origins(articles []model.Article, primary func(sourceID string) bool) []origin.Origin {
	byEvent := map[string][]int{}
	for i, a := range articles {
		id := EventID(a)
		byEvent[id] = append(byEvent[id], i)
	}
	out := make([]origin.Origin, len(articles))
	for _, idx := range byEvent {
		ins := make([]origin.Input, len(idx))
		os := make([]origin.Origin, len(idx))
		for k, i := range idx {
			ins[k] = OriginInput(articles[i], primary)
			os[k] = origin.Detect(ins[k])
		}
		for k, o := range origin.Link(ins, os) {
			out[idx[k]] = o
		}
	}
	return out
}

// recordOrigins stores the origins of the articles that are evidence of the
// events a batch touched. The other articles of those events, stored earlier,
// are read too, so that a late copy is joined to the report it copies. A failure
// is logged and repaired by `events rebuild`; it never loses news.
func (p *Pipeline) recordOrigins(ctx context.Context, batch []model.Article, events []store.EventInput) {
	st, ok := p.cfg.Store.(originStore)
	if !ok {
		return
	}
	byID := map[string]model.Article{}
	for _, a := range batch {
		byID[a.ID] = a
	}
	all := map[string]model.Article{}
	for _, e := range events {
		for _, ev := range e.Evidence {
			if a, ok := byID[ev.ArticleID]; ok {
				all[a.ID] = a
			}
		}
		hist, err := st.EventHistory(ctx, e.EventID)
		if err != nil || len(hist) == 0 {
			continue
		}
		for _, ev := range hist[len(hist)-1].Evidence {
			if _, have := all[ev.ArticleID]; have {
				continue
			}
			if old, err := st.Get(ctx, ev.ArticleID); err == nil {
				all[old.ID] = old
			}
		}
	}
	if len(all) == 0 {
		return
	}
	arts := make([]model.Article, 0, len(all))
	for _, id := range slices.Sorted(maps.Keys(all)) {
		arts = append(arts, all[id])
	}
	if _, err := st.PutOrigins(ctx, Origins(arts, p.cfg.Primary), p.cfg.Now()); err != nil {
		p.cfg.Logger.Warn("pipeline: record origins failed; repair them with: stoxicity events rebuild", "err", err)
	}
}
