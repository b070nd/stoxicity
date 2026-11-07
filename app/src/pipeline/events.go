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
func Events(articles []model.Article) []store.EventInput { return EventsFrom(articles, nil) }

// Claims reads what an article claims: one assertion for each event it reports,
// each with the companies it is about and the sentence it comes from.
func Claims(a model.Article, names func(ticker string) []string, rating func(items []string) (model.ItemRating, bool)) assertion.Result {
	return assertion.Extract(assertion.Input{
		ArticleID: a.ID, Title: a.Title, Summary: a.Summary, Form: a.Form, Items: a.Items, Tickers: a.Tickers,
		Published: a.Published, Score: a.Score, Names: names, ItemRating: rating,
	})
}

// EventsFrom reads the events out of articles whose claims have been read. An
// article gives an event only when one of its claims is a current event about a
// company (not negated, denied, past, a preview, or about nobody); the claim that
// matters most names the event and gives its materiality, so that a story that
// says two things is not named after whichever keyword comes first. An article
// without claims in the map is read as Events always did, from the lens's score.
func EventsFrom(articles []model.Article, claims map[string]assertion.Result) []store.EventInput {
	var out []store.EventInput
	at := map[string]int{}
	for _, a := range articles {
		a, roles, ok := eventReading(a, claims)
		if !ok {
			continue
		}
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
		if seen && a.Score.Materiality > e.Materiality { // the most material evidence names the event
			e.Kind, e.Title, e.Materiality, e.Relevance, e.LensVersion = a.Score.Kind, a.Title, a.Score.Materiality, a.Score.Relevance, a.Score.LensVersion
			e.Rationale = strings.Join(a.Score.Reasons, "; ")
		}
		e.Evidence = append(e.Evidence, model.Evidence{ArticleID: a.ID, SourceID: a.SourceID})
		for _, tk := range a.Tickers {
			role, has := roles[tk]
			if !has {
				role = RoleOf(a, tk)
			}
			e.Securities = append(e.Securities, model.EventSecurity{Ticker: tk, Role: role})
		}
		for _, t := range a.Topics {
			if !slices.Contains(e.Topics, t) {
				e.Topics = append(e.Topics, t)
			}
		}
	}
	return out
}

// eventReading is the article as the event reads it: with the kind, materiality
// and relevance of its most important current claim when its claims are known,
// and the roles those claims give the companies. ok is false when the article
// is no evidence of an event.
func eventReading(a model.Article, claims map[string]assertion.Result) (model.Article, map[string]model.Role, bool) {
	res, have := claims[a.ID]
	if !have {
		return a, nil, isEvent(a)
	}
	best, mat, live := res.Best()
	if !live {
		return a, nil, false
	}
	if best.Kind == model.EventOther && mat < minOtherMateriality {
		return a, nil, false
	}
	roles := map[string]model.Role{}
	for _, x := range res.Assertions {
		if !x.Live() {
			continue
		}
		for _, p := range x.Parties {
			r := model.RoleMentioned
			switch p.Role {
			case assertion.Subject:
				r = model.RoleSubject
			case assertion.Counterparty:
				r = model.RoleCounterparty
			}
			if roles[p.Ticker].Strength() < r.Strength() {
				roles[p.Ticker] = r
			}
		}
	}
	// The lens rated the first keyword; the claim that matters most moves the
	// relevance by the difference, at the lens's own 0.15 a level.
	if best.Kind != a.Score.Kind || mat != a.Score.Materiality {
		rel := a.Score.Relevance + 0.15*float64(mat-a.Score.Materiality)
		rel = math.Min(math.Max(rel, 0), 1)
		if lens.Noise(best.Kind) && rel > 0.35 {
			rel = 0.35
		}
		a.Score.Relevance = math.Round(rel*1000) / 1000
	}
	reasons := slices.Clone(a.Score.Reasons)
	a.Score.Reasons = append(reasons, fmt.Sprintf("claim %d of %d: %s (%s)", best.Seq, len(res.Assertions), best.Kind, best.Match))
	a.Score.Kind, a.Score.Materiality = best.Kind, mat
	if a.Score.LensVersion != "" {
		a.Score.LensVersion += "+" + res.Version
	}
	return a, roles, true
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
	in := EventsFrom(batch, claims)
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
	p.recordOrigins(ctx, batch, claims)
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

// recordOrigins stores the origins of the articles of the events a batch
// touched. An event's other articles, stored earlier, are read too, so that a
// late copy is joined to the report it copies. A failure is logged and repaired
// by `events rebuild`; it never loses news.
func (p *Pipeline) recordOrigins(ctx context.Context, batch []model.Article, claims map[string]assertion.Result) {
	st, ok := p.cfg.Store.(originStore)
	if !ok {
		return
	}
	all := map[string]model.Article{}
	evidence := func(a model.Article) bool { _, _, ok := eventReading(a, claims); return ok }
	for _, a := range batch {
		if evidence(a) {
			all[a.ID] = a
		}
	}
	for _, a := range slices.Clone(batch) {
		if !evidence(a) {
			continue
		}
		hist, err := st.EventHistory(ctx, EventID(a))
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
