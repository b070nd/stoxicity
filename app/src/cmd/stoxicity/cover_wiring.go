package main

import (
	"context"
	"errors"
	"time"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/refdata"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/universe"
	"github.com/b070nd/stoxicity/app/src/valuation"
)

// coverKeeper is the keeper of the share count of each class of stock; nil when
// fundamentals are off or there is no client to ask with. It is made on the
// command's own goroutine and what it needs from the app is taken here, because
// the keeper runs in the background cycle.
func (a *app) coverKeeper(db *store.DB, client httpx.Client) *fundamentals.CoverKeeper {
	c := a.cfg.Company
	if client == nil || c.Fundamentals != "sec" {
		return nil
	}
	w := &coverWiring{db: db, layer: a.layerPath(), sectors: a.sectorMap(), known: a.knownTickers(), threshold: a.cfg.Lens.Threshold, now: a.now}
	return &fundamentals.CoverKeeper{
		Store: db, Client: client, SubmissionsURL: c.SubmissionsURL, Archives: a.fundamentalsArchives(),
		Candidates: w.candidates, Map: w.mapClasses, Now: a.now,
	}
}

// coverWiring gives the keeper the companies and listings of the universe as of
// the start of a cycle.
type coverWiring struct {
	db        *store.DB
	layer     string
	sectors   map[string]string
	known     []string
	threshold float64
	now       func() time.Time
	univ      *universe.Universe
}

func (w *coverWiring) build(ctx context.Context) error {
	u, err := refdata.Build(ctx, w.db, w.layer, w.sectors, w.known)
	if u == nil {
		if err == nil {
			err = errors.New("no company list")
		}
		return err
	}
	w.univ = u
	return nil
}

func (w *coverWiring) candidates(ctx context.Context) ([]fundamentals.CoverCandidate, error) {
	if err := w.build(ctx); err != nil {
		return nil, err
	}
	listed := map[string]int{}
	for _, p := range w.univ.All() {
		if p.CIK != "" && p.Type() == universe.TypeStock {
			listed[p.CIK]++
		}
	}
	prio := map[string]bool{}
	for _, t := range interestingTickers(ctx, w.db, w.known, w.layer, w.threshold, w.now()) {
		if p, ok := w.univ.Get(t); ok && p.CIK != "" {
			prio[p.CIK] = true
		}
	}
	var out []fundamentals.CoverCandidate
	seen := map[string]bool{}
	for _, p := range w.univ.All() {
		if p.CIK == "" || p.Type() != universe.TypeStock || seen[p.CIK] {
			continue
		}
		seen[p.CIK] = true
		out = append(out, fundamentals.CoverCandidate{CIK: p.CIK, Ticker: p.Ticker, Listings: listed[p.CIK], Priority: prio[p.CIK]})
	}
	return out, nil
}

// mapClasses maps the issuer's classes to the securities of its listed stock.
func (w *coverWiring) mapClasses(ctx context.Context, cik string, at time.Time) error {
	if w.univ == nil {
		if err := w.build(ctx); err != nil {
			return err
		}
	}
	var ls []valuation.Listing
	for _, p := range w.univ.All() {
		if p.CIK != cik || (p.Type() != universe.TypeStock && p.Type() != universe.TypeADR) {
			continue
		}
		id, err := w.db.SecurityOf(ctx, p.Ticker, at)
		if err != nil {
			return err
		}
		ls = append(ls, valuation.Listing{SecurityID: id, Ticker: p.Ticker, Type: p.Type()})
	}
	_, err := w.db.MapClasses(ctx, cik, ls, at)
	return err
}
