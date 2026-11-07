package pipeline

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/model"
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
func Events(articles []model.Article) []store.EventInput {
	var out []store.EventInput
	at := map[string]int{}
	for _, a := range articles {
		if !isEvent(a) {
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
	in := Events(batch)
	if len(in) == 0 {
		return
	}
	if _, err := ev.PutEvents(ctx, in, p.cfg.Now()); err != nil {
		p.cfg.Logger.Warn("pipeline: record events failed; rebuild them with: stoxicity events rebuild", "err", err)
	}
}
