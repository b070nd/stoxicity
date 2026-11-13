package gold

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// CategoryOf is the category an event kind belongs to for the draw: the family
// of economic events its text is read for. Kinds the text rules do not read
// have none.
func CategoryOf(kind string) (eventfact.Category, bool) {
	switch model.EventKind(kind) {
	case model.EventResults:
		return eventfact.Earnings, true
	case model.EventGuidance:
		return eventfact.Guidance, true
	case model.EventBuyback:
		return eventfact.Buyback, true
	case model.EventAgreement, model.EventProduct:
		return eventfact.Contract, true
	case model.EventAnalystTgt:
		return eventfact.Analyst, true
	}
	return "", false
}

// Categories lists the categories a draw can be restricted to, sorted.
func Categories() []eventfact.Category {
	return []eventfact.Category{eventfact.Analyst, eventfact.Buyback, eventfact.Contract, eventfact.Earnings, eventfact.Guidance}
}

// Candidate is a stored event that may be drawn for labelling.
type Candidate struct {
	ID        string // the stored event's ID; an event is drawn once whatever its securities
	Symbol    string
	Kind      string
	Published time.Time
	Passages  []Passage
}

// Source is what Candidates needs of the database; *store.DB satisfies it.
type Source interface {
	SignalEvents(ctx context.Context, q store.SignalQuery) ([]store.SignalEvent, error)
	Range(ctx context.Context, from, to time.Time) ([]model.Article, error)
}

// Candidates reads the stored events made public in [from, to) whose kind has a
// category, with the text the extractors would read: the title of each piece of
// evidence and the summary of its article, oldest evidence first. An event that
// has no text is left out. Nothing is written.
func Candidates(ctx context.Context, s Source, from, to time.Time) ([]Candidate, error) {
	events, err := s.SignalEvents(ctx, store.SignalQuery{From: from, To: to})
	if err != nil {
		return nil, fmt.Errorf("gold: events: %w", err)
	}
	arts, err := s.Range(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("gold: articles: %w", err)
	}
	articles := make(map[string]model.Article, len(arts))
	for _, a := range arts {
		articles[a.ID] = a
	}
	var out []Candidate
	for _, e := range events {
		if _, ok := CategoryOf(e.Kind); !ok || len(e.Evidence) == 0 {
			continue
		}
		ev := slices.Clone(e.Evidence)
		sort.SliceStable(ev, func(i, j int) bool {
			if !ev[i].Published.Equal(ev[j].Published) {
				return ev[i].Published.Before(ev[j].Published)
			}
			return ev[i].ArticleID < ev[j].ArticleID
		})
		var passages []Passage
		seen := map[string]bool{}
		for _, x := range ev {
			if seen[x.ArticleID] {
				continue
			}
			seen[x.ArticleID] = true
			if x.Title != "" {
				passages = append(passages, Passage{Ref: x.ArticleID, Text: x.Title})
			}
			if a, ok := articles[x.ArticleID]; ok && a.Summary != "" {
				passages = append(passages, Passage{Ref: x.ArticleID, Text: a.Summary})
			}
		}
		if len(passages) == 0 {
			continue
		}
		out = append(out, Candidate{ID: e.EventID, Symbol: e.Ticker, Kind: e.Kind, Published: ev[0].Published, Passages: passages})
	}
	return out, nil
}

// Rule says what to draw.
type Rule struct {
	N        int       // how many events
	Seed     int64     // the seed of the order they are drawn in
	Category string    // draw only this category; empty draws from all of them
	From, To time.Time // the window the candidates were read from, recorded in the file
}

// rank is the position of an ID in the order a seed gives to a purpose: the
// first eight bytes of a digest of the three, so the order is the same on every
// run and machine and does not depend on the order the IDs arrive in.
func rank(purpose string, seed int64, id string) uint64 {
	h := sha256.New()
	h.Write([]byte(purpose))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(seed, 10)))
	h.Write([]byte{0})
	h.Write([]byte(id))
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

// Draw chooses up to rule.N of the candidates for labelling, in the order the
// seed gives to the event IDs, and returns them as a file of items with empty
// labels. An event is never drawn twice: candidates with one ID count once (the
// first by symbol), and an ID in already is skipped, so that a later draw does
// not repeat an earlier one. It draws fewer than N when fewer are eligible and
// says so in the header; it fails when none is.
func Draw(cands []Candidate, rule Rule, already map[string]bool) (File, error) {
	if rule.N < 1 {
		return File{}, errors.New("gold: draw at least one item")
	}
	if rule.Category != "" && !slices.Contains(Categories(), eventfact.Category(rule.Category)) {
		return File{}, fmt.Errorf("gold: %q is not a category that is drawn (%v)", rule.Category, Categories())
	}
	sorted := slices.Clone(cands)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].ID != sorted[j].ID {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].Symbol < sorted[j].Symbol
	})
	var pool []Candidate
	seen := map[string]bool{}
	excluded := 0
	for _, c := range sorted {
		cat, ok := CategoryOf(c.Kind)
		switch {
		case !ok, c.ID == "", len(c.Passages) == 0, seen[c.ID]:
			continue
		case rule.Category != "" && string(cat) != rule.Category:
			continue
		}
		seen[c.ID] = true
		if already[c.ID] {
			excluded++
			continue
		}
		pool = append(pool, c)
	}
	if len(pool) == 0 {
		return File{}, errors.New("gold: no event is eligible: none stored in the window, or all of them are already drawn")
	}
	sort.SliceStable(pool, func(i, j int) bool {
		return rank("draw", rule.Seed, pool[i].ID) < rank("draw", rule.Seed, pool[j].ID)
	})
	take := min(rule.N, len(pool))
	items := make([]Item, 0, take)
	for _, c := range pool[:take] {
		cat, _ := CategoryOf(c.Kind)
		items = append(items, Item{
			ID: c.ID, Symbol: c.Symbol, Kind: c.Kind, Category: string(cat), Published: c.Published.UTC(),
			Text: c.Passages, Records: []Record{},
		})
	}
	return File{
		Header: Header{Format: Version, Drawn: &Drawn{
			Seed: rule.Seed, N: rule.N, Category: rule.Category, From: rule.From.UTC(), To: rule.To.UTC(),
			Eligible: len(pool), Excluded: excluded,
			Rule: "stored events of a kind that has a category, one per event ID, without those already drawn, " +
				"ordered by the SHA-256 of purpose, seed and event ID; the first N are taken",
		}},
		Items: items,
	}, nil
}
