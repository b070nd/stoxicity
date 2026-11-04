// Package workspace opens the configuration, the stores and one tenant for the
// interactive programs, and implements tui.Backend over them. The stoxicity
// command does the same start-up in package main; this copy exists so the TUI
// binary does not depend on a main package.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/envfile"
	"github.com/b070nd/stoxicity/app/src/filter"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/reader"
	"github.com/b070nd/stoxicity/app/src/sources"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/tenant"
	"github.com/b070nd/stoxicity/app/src/tui"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// Options says where to start and what to open.
type Options struct {
	Root   string // configuration folder; empty means search upwards from Cwd
	Cwd    string // where to search from; empty means the working directory
	Tenant string // empty means $STOXICITY_TENANT, then the configured default
	Watch  string // empty means the tenant's default_watch
	Now    func() time.Time
}

// Workspace is an open tenant. Close it when done.
type Workspace struct {
	cfg      config.Config
	reg      sources.Registry
	db       *store.DB
	ts       store.TenantStore
	tenant   tenant.Tenant
	read     *reader.Reader
	watches  watch.FSStore
	now      func() time.Time
	zone     *time.Location
	resolver *company.Resolver
	watchID  string

	mu      sync.Mutex
	byID    map[string]watch.Watch
	baseDir string
}

var _ tui.Backend = (*Workspace)(nil)

// Open reads the configuration and opens the tenant's data. It does not fetch
// anything and does not write.
func Open(opts Options) (*Workspace, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	root := opts.Root
	if root == "" {
		if root = findUp(cwd, hasConfig); root == "" {
			root = cwd
		}
	}
	cfgDir := config.Dir(root)
	envDir := findUp(cwd, hasEnvFile)
	base := envDir
	if base == "" {
		base = root
	}

	var allow []string
	if reg, err := sources.Load(cfgDir); err == nil {
		for _, s := range reg {
			if s.AuthEnv != "" {
				allow = append(allow, s.AuthEnv)
			}
		}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := envfile.Setup(nil, base, allow, quiet); err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	reg, err := sources.Load(cfgDir)
	if err != nil {
		return nil, err
	}

	tenantsDir := cfg.Tenancy.Dir
	if !filepath.IsAbs(tenantsDir) {
		tenantsDir = filepath.Join(cfgDir, tenantsDir)
	}
	id := tenant.Resolve(opts.Tenant, os.Getenv("STOXICITY_TENANT"), cfg.Tenancy.Default)
	t, err := tenant.FSRegistry{Dir: tenantsDir}.Get(id)
	if err != nil {
		return nil, err
	}

	dataDir := cfg.DataDir
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(base, dataDir)
	}
	db, err := store.Open(dataDir)
	if err != nil {
		return nil, err
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	zone := cfg.Location()
	master, err := company.Load(filepath.Join(dataDir, "company"))
	if err != nil {
		master = company.Master{} // a missing or damaged cache only costs names
	}
	w := &Workspace{
		cfg: cfg, reg: reg, db: db, ts: db.Tenant(t.ID), tenant: t,
		watches: watch.FSStore{Dir: filepath.Join(tenantsDir, t.ID, "watches")},
		now:     now, zone: zone, byID: map[string]watch.Watch{}, baseDir: base,
	}
	w.read = reader.New(db, t.ID, reg.VisibleTo(t.ID), zone,
		reader.WithNow(now), reader.WithSince(cfg.SinceTime()))
	extra := slices.Clone(cfg.Watchlist)
	if ws, err := w.watches.List(); err == nil {
		for _, x := range ws {
			extra = append(extra, x.Scope.Tickers...)
		}
	}
	w.resolver = company.NewResolver(master, extra)
	w.watchID = opts.Watch
	if w.watchID == "" {
		w.watchID = t.DefaultWatch
	}
	return w, nil
}

// Close releases the database.
func (w *Workspace) Close() error { return w.db.Close() }

func (w *Workspace) Tenant() string       { return w.tenant.ID }
func (w *Workspace) Zone() *time.Location { return w.zone }
func (w *Workspace) Threshold() float64   { return w.cfg.Lens.Threshold }
func (w *Workspace) Now() time.Time       { return w.now() }
func (w *Workspace) DefaultWatch() string { return w.watchID }
func (w *Workspace) Watchlist() []string  { return slices.Clone(w.cfg.Watchlist) }
func (w *Workspace) CompanyName(t string) string {
	if e, ok := w.resolver.Lookup(t); ok {
		return e.Name
	}
	return ""
}

// TopicNames are the topics the configuration defines.
func (w *Workspace) TopicNames() []string {
	out := make([]string, 0, len(w.cfg.Topics))
	for _, t := range w.cfg.Topics {
		out = append(out, t.Name)
	}
	return out
}

// Watches lists the tenant's watches with their unread counts.
func (w *Workspace) Watches(ctx context.Context) ([]tui.WatchInfo, error) {
	list, err := w.watches.List()
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	clear(w.byID)
	for _, x := range list {
		w.byID[x.ID] = x
	}
	w.mu.Unlock()
	out := make([]tui.WatchInfo, 0, len(list))
	for _, x := range list {
		n, err := w.ts.Unread(ctx, x.ID)
		if err != nil {
			return nil, err
		}
		name := x.Name
		if name == "" {
			name = x.ID
		}
		out = append(out, tui.WatchInfo{ID: x.ID, Name: name, Unread: n})
	}
	return out, nil
}

// watchFor returns the watch with the ID. An empty ID means no watch.
func (w *Workspace) watchFor(id string) (watch.Watch, bool, error) {
	if id == "" {
		return watch.Watch{}, false, nil
	}
	w.mu.Lock()
	x, ok := w.byID[id]
	w.mu.Unlock()
	if ok {
		return x, true, nil
	}
	x, err := w.watches.Get(id)
	if err != nil {
		return watch.Watch{}, false, err
	}
	w.mu.Lock()
	w.byID[id] = x
	w.mu.Unlock()
	return x, true, nil
}

// matcher builds the predicate of a watch, or nil for all news.
func (w *Workspace) matcher(id string) (func(model.Article) bool, error) {
	x, ok, err := w.watchFor(id)
	if err != nil || !ok {
		return nil, err
	}
	opts := watch.Options{DefaultThreshold: w.cfg.Lens.Threshold}
	if x.Query != "" {
		q, err := filter.Parse(x.Query)
		if err != nil {
			return nil, fmt.Errorf("watch %s: %w", id, err)
		}
		opts.Query = func(a model.Article) bool { return q.MatchIn(a, w.zone) }
	}
	return func(a model.Article) bool { return x.Match(a, opts) }, nil
}

// Articles returns what the watch matches in [from, to), newest first.
func (w *Workspace) Articles(ctx context.Context, watchID string, from, to time.Time) ([]model.Article, error) {
	match, err := w.matcher(watchID)
	if err != nil {
		return nil, err
	}
	arts, err := w.read.Range(ctx, from, to)
	if err != nil || match == nil {
		return arts, err
	}
	return slices.DeleteFunc(arts, func(a model.Article) bool { return !match(a) }), nil
}

// Search runs a Google-style query over what the watch matches.
func (w *Workspace) Search(ctx context.Context, watchID, query string, from, to time.Time, limit int) ([]model.Article, error) {
	q, err := filter.Parse(query)
	if err != nil {
		return nil, err
	}
	match, err := w.matcher(watchID)
	if err != nil {
		return nil, err
	}
	if match == nil {
		res, err := w.read.Search(ctx, q, from, to, limit)
		return res.Articles, err
	}
	// The watch narrows the result after the search, so ask for more than the
	// limit and cut afterwards.
	res, err := w.read.Search(ctx, q, from, to, max(limit, 1)*4)
	if err != nil {
		return nil, err
	}
	out := res.Articles[:0]
	for _, a := range res.Articles {
		if match(a) {
			out = append(out, a)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Sources reports every configured source with its last known health.
func (w *Workspace) Sources(ctx context.Context) ([]tui.SourceRow, error) {
	var rows []tui.SourceRow
	for _, id := range slices.Sorted(maps.Keys(w.reg)) {
		s := w.reg[id]
		_, h, err := w.db.LoadSource(ctx, id)
		if err != nil {
			return nil, err
		}
		row := tui.SourceRow{
			ID: id, Kind: string(s.Kind), Enabled: s.Enabled, Licence: string(s.Licence),
			LastOK: h.LastOK, LastError: h.LastError,
		}
		if !h.LastOK.IsZero() || h.Failures > 0 {
			row.Health = h.Level
			if row.Health == "" {
				row.Health = "yellow"
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Verdicts are the user's keep/drop labels by article ID.
func (w *Workspace) Verdicts(ctx context.Context) (map[string]labels.Verdict, error) {
	all, err := w.ts.Labels().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]labels.Verdict, len(all))
	for _, l := range all {
		out[l.ArticleID] = l.Verdict
	}
	return out, nil
}

// Label records a keep or drop verdict on an article the tenant can see.
func (w *Workspace) Label(ctx context.Context, articleID string, keep bool) error {
	a, err := w.read.Get(ctx, articleID)
	if err != nil {
		return err
	}
	v := labels.Drop
	if keep {
		v = labels.Keep
	}
	return w.ts.Labels().Put(ctx, labels.Label{
		TenantID: w.tenant.ID, ArticleID: a.ID, Verdict: v,
		At: w.now(), LensVersion: a.Score.LensVersion,
	})
}

// MarkSeen moves the watch's read marker to now.
func (w *Workspace) MarkSeen(ctx context.Context, watchID string) error {
	if watchID == "" {
		return errors.New("workspace: no watch to mark")
	}
	return w.ts.MarkSeen(ctx, watchID, w.now())
}

func findUp(dir string, match func(string) bool) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if match(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func hasConfig(dir string) bool {
	for _, p := range []string{"app/config/config.yaml", "config/config.yaml", "config.yaml"} {
		if exists(filepath.Join(dir, p)) {
			return true
		}
	}
	return false
}

func hasEnvFile(dir string) bool {
	return exists(filepath.Join(dir, ".env")) || exists(filepath.Join(dir, ".env.local"))
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
