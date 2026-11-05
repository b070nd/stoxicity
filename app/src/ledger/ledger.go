// Package ledger owns the path from detection to delivery. A watch is a saved
// view over the shared base; the ledger is what turns the base into a tenant's
// events:
//
//	fetched articles → hits (Evaluate) → notifications (Deliver, Digests)
//
// Evaluate looks only at the articles fetched since it last looked at a watch
// (a cursor per watch, kept in the tenant's database) and stamps each hit with
// the time of detection. That time, not the publication time of the article, is
// what the read marker is compared with, so an article that is fetched after the
// user last looked is unread and can alert. Deliver sends the alerts of hits
// that have not been delivered, one notification per watch and alert, and
// remembers what it sent so that nothing is sent twice. Digests sends the daily
// and weekly digests of the watches that ask for them.
//
// Every read goes through the tenant's visible sources, so a tenant never gets
// a hit, a notification or a digest for a source its licence does not allow.
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/reader"
	"github.com/b070nd/stoxicity/app/src/sources"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/watch"
)

const (
	// defaultWindow is how far back the first evaluation of a watch looks.
	defaultWindow = 48 * time.Hour
	// pageSize is how many articles Evaluate asks the store for at a time.
	pageSize = 500
	// futureSlack is how far past now an article may be published and still be
	// a candidate in Preview.
	futureSlack = 24 * time.Hour
)

// Config carries what a Ledger works with.
type Config struct {
	// Store is the database. Required.
	Store store.Store
	// Sources decides which sources a tenant may see.
	Sources sources.Registry
	// Watches returns the watch store of a tenant. Required.
	Watches func(tenantID string) watch.Store
	// Channels are the senders by kind. The TUI needs none. An alert that names
	// a kind that is not here is treated as delivered, with a warning in the
	// log, so its hits do not wait for ever.
	Channels map[channel.Kind]channel.Channel
	// Threshold is the relevance a watch uses when it sets no lens.threshold.
	Threshold float64
	// Zone is the time zone of quiet hours, of digest times, and in which a
	// watch query reads after: and before: days. Nil means UTC.
	Zone *time.Location
	// Now is the clock; time.Now if nil.
	Now func() time.Time
	// Logger receives debug and warning lines. Nil discards them.
	Logger *slog.Logger
	// FilterEnv gives the extra search operators (company:, list:, tag:) of a
	// tenant to its watch queries. Nil means the plain filter language.
	FilterEnv func(tenantID string) filter.Env
	// Window is how far back a watch is evaluated the first time, and again
	// after its definition changed, measured in fetch time. Zero means 48 hours.
	Window time.Duration
}

// Ledger evaluates watches into hits and delivers what the hits call for. It
// holds no state of its own: cursors, deliveries and sent digests live in the
// tenant's database, so a new Ledger in a new process carries on where the last
// one stopped. It is safe for concurrent use as far as the Store and the
// channels are, but two processes working on one tenant at once may send the
// same notification twice.
type Ledger struct {
	cfg Config
}

// New returns a Ledger using cfg.
func New(cfg Config) *Ledger {
	if cfg.Zone == nil {
		cfg.Zone = time.UTC
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Window <= 0 {
		cfg.Window = defaultWindow
	}
	return &Ledger{cfg: cfg}
}

// Evaluate runs the watches of a tenant over the articles the tenant may see
// that were fetched since the watch was last evaluated, and records the hits,
// stamped with the current time. It returns the number of hits that were new for
// each watch, so running it twice with no new article records and reports
// nothing the second time.
//
// A watch with no cursor, or whose definition changed since its cursor was
// set (scope, query, lens, alerts, or the default threshold), starts again at
// Window before now. A hit recorded earlier for the same watch and article is
// left as it is. A watch whose query does not parse is skipped and reported in
// the error; the other watches are still evaluated and recorded.
//
// The cursor is the fetch time of the newest article seen and a later poll
// stamps its articles with a later time, so an article that is stored with a
// fetch time earlier than one already evaluated is not seen. The fetchers stamp
// the start of a poll; the store saves a poll's batch in one transaction.
func (l *Ledger) Evaluate(ctx context.Context, tenantID string) (map[string]int, error) {
	watches, err := l.watches(tenantID)
	if err != nil {
		return nil, err
	}
	ts := l.cfg.Store.Tenant(tenantID)
	allowed := l.cfg.Sources.VisibleTo(tenantID)
	now := l.cfg.Now()
	counts := make(map[string]int, len(watches))
	var errs []error
	for _, w := range watches {
		n, err := l.evaluate(ctx, ts, allowed, w, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		counts[w.ID] = n
	}
	return counts, errors.Join(errs...)
}

// parseQuery reads a watch query with the tenant's extra operators.
func (l *Ledger) parseQuery(tenantID, expr string) (filter.Query, error) {
	var env filter.Env
	if l.cfg.FilterEnv != nil {
		env = l.cfg.FilterEnv(tenantID)
	}
	return filter.ParseWith(expr, env)
}

// evaluate brings one watch up to date and returns the number of new hits.
func (l *Ledger) evaluate(ctx context.Context, ts store.TenantStore, allowed map[string]bool, w watch.Watch, now time.Time) (int, error) {
	q, err := l.parseQuery(ts.ID(), w.Query)
	if err != nil {
		return 0, fmt.Errorf("ledger: watch %q: query: %w", w.ID, err)
	}
	hash := definitionHash(w, l.cfg.Threshold)
	cur, found, err := ts.Cursor(ctx, w.ID)
	if err != nil {
		return 0, fmt.Errorf("ledger: watch %q: %w", w.ID, err)
	}
	after := now.Add(-l.cfg.Window)
	if found && cur.Hash == hash {
		after = cur.Fetched
	}

	opts := watch.Options{
		DefaultThreshold: l.cfg.Threshold,
		Query:            func(a model.Article) bool { return q.MatchIn(a, l.cfg.Zone) },
		Detected:         now,
	}
	added := 0
	newest := after
	wrote := false
	for {
		page, err := l.cfg.Store.FetchedAfter(ctx, newest, allowed, pageSize)
		if err != nil {
			return added, fmt.Errorf("ledger: watch %q: %w", w.ID, err)
		}
		if len(page) == 0 {
			break
		}
		n, err := ts.AddHits(ctx, watch.Evaluate(w, page, opts))
		if err != nil {
			return added, fmt.Errorf("ledger: watch %q: %w", w.ID, err)
		}
		added += n
		newest = page[len(page)-1].Fetched
		if err := ts.SetCursor(ctx, w.ID, store.Cursor{Fetched: newest, Hash: hash}); err != nil {
			return added, fmt.Errorf("ledger: watch %q: %w", w.ID, err)
		}
		wrote = true
		if len(page) < pageSize {
			break
		}
	}
	if !wrote && (!found || cur.Hash != hash) {
		// Nothing new, but the watch has a cursor now, and its new definition.
		if err := ts.SetCursor(ctx, w.ID, store.Cursor{Fetched: newest, Hash: hash}); err != nil {
			return added, fmt.Errorf("ledger: watch %q: %w", w.ID, err)
		}
	}
	l.cfg.Logger.Debug("ledger: evaluated", "tenant", ts.ID(), "watch", w.ID, "new_hits", added)
	return added, nil
}

// Preview returns the articles published since that the tenant may see and that
// w matches, newest first, without recording anything. It is how a watch is
// tried before it is saved.
func (l *Ledger) Preview(ctx context.Context, tenantID string, w watch.Watch, since time.Time) ([]model.Article, error) {
	if !model.ValidID(tenantID) {
		return nil, fmt.Errorf("ledger: invalid tenant ID %q", tenantID)
	}
	if l.cfg.Store == nil {
		return nil, errors.New("ledger: no store configured")
	}
	q, err := l.parseQuery(tenantID, w.Query)
	if err != nil {
		return nil, fmt.Errorf("ledger: watch %q: query: %w", w.ID, err)
	}
	articles, err := l.reader(tenantID).Range(ctx, since, l.cfg.Now().Add(futureSlack))
	if err != nil {
		return nil, fmt.Errorf("ledger: load articles: %w", err)
	}
	opts := l.matchOptions(q)
	var out []model.Article
	for _, a := range articles {
		if w.Match(a, opts) {
			out = append(out, a)
		}
	}
	return out, nil
}

// reader returns the tenant's licence-checked view of the articles.
func (l *Ledger) reader(tenantID string) *reader.Reader {
	return reader.New(l.cfg.Store, tenantID, l.cfg.Sources.VisibleTo(tenantID), l.cfg.Zone, reader.WithNow(l.cfg.Now))
}

// matchOptions are the options with which a watch is matched, without a
// detection time.
func (l *Ledger) matchOptions(q filter.Query) watch.Options {
	return watch.Options{
		DefaultThreshold: l.cfg.Threshold,
		Query:            func(a model.Article) bool { return q.MatchIn(a, l.cfg.Zone) },
	}
}

// watches checks the tenant ID and the configuration and loads the tenant's
// watches.
func (l *Ledger) watches(tenantID string) ([]watch.Watch, error) {
	if !model.ValidID(tenantID) {
		return nil, fmt.Errorf("ledger: invalid tenant ID %q", tenantID)
	}
	if l.cfg.Store == nil {
		return nil, errors.New("ledger: no store configured")
	}
	if l.cfg.Watches == nil {
		return nil, errors.New("ledger: no watch store configured")
	}
	ws, err := l.cfg.Watches(tenantID).List()
	if err != nil {
		return nil, fmt.Errorf("ledger: load watches of tenant %q: %w", tenantID, err)
	}
	return ws, nil
}

// definitionHash is a stable hash of what decides which articles a watch hits
// and with which alerts: its scope, query, lens and the conditions of its
// alerts, together with the default threshold. Name, description, dashboard,
// channels, quiet hours, cooldowns and digest do not change what is hit, so
// editing them does not start the watch over.
func definitionHash(w watch.Watch, threshold float64) string {
	type alertDef struct {
		Name string
		When watch.When
	}
	def := struct {
		Scope     watch.Scope
		Query     string
		Lens      watch.Lens
		Alerts    []alertDef
		Threshold float64
	}{Scope: w.Scope, Query: w.Query, Lens: w.Lens, Threshold: threshold}
	for _, a := range w.Alerts {
		def.Alerts = append(def.Alerts, alertDef{a.Name, a.When})
	}
	b, _ := json.Marshal(def) // plain strings and numbers always marshal
	sum := sha256.Sum256(b)
	return "w1-" + hex.EncodeToString(sum[:8])
}
