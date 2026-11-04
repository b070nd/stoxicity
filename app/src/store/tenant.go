package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// Tenant returns the handle for one tenant. The tenant's database file is
// created on first use. If id is not a valid ID (model.ValidID), every method
// of the returned TenantStore fails and nothing is created on disk, which also
// blocks path tricks such as "../x".
func (db *DB) Tenant(id string) TenantStore {
	return &tenantStore{db: db, id: id, valid: model.ValidID(id)}
}

type tenantStore struct {
	db    *DB
	id    string
	valid bool
}

var _ TenantStore = (*tenantStore)(nil)

// handle returns this tenant's open database.
func (t *tenantStore) handle() (*handle, error) {
	if !t.valid {
		return nil, fmt.Errorf("store: invalid tenant id %q", t.id)
	}
	return t.db.tenantHandle(t.id)
}

// ID returns the tenant's ID.
func (t *tenantStore) ID() string { return t.id }

// Labels returns this tenant's label store.
func (t *tenantStore) Labels() labels.Store { return tenantLabels{t} }

// PutHits records hits in one transaction. A hit for a (watch, article) pair
// that is already recorded is left untouched. A zero At is stored as now.
func (t *tenantStore) PutHits(ctx context.Context, hits []watch.Hit) error {
	_, err := t.AddHits(ctx, hits)
	return err
}

// AddHits is PutHits that returns the number of hits that were new.
func (t *tenantStore) AddHits(ctx context.Context, hits []watch.Hit) (int, error) {
	h, err := t.handle()
	if err != nil {
		return 0, err
	}
	for _, hit := range hits {
		if hit.WatchID == "" || hit.ArticleID == "" {
			return 0, errors.New("store: put hits: hit without watch or article ID")
		}
	}
	if len(hits) == 0 {
		return 0, nil
	}
	now := t.db.now()
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: put hits: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO hits (watch_id, article_id, at_ms, alerts) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("store: put hits: %w", err)
	}
	defer stmt.Close()
	added := 0
	for _, hit := range hits {
		at := hit.At
		if at.IsZero() {
			at = now
		}
		res, err := stmt.ExecContext(ctx, hit.WatchID, hit.ArticleID, ms(at), encodeList(hit.Alerts))
		if err != nil {
			return 0, fmt.Errorf("store: put hits: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("store: put hits: %w", err)
		}
		added += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: put hits: %w", err)
	}
	return added, nil
}

// Hits returns the watch's hits at or after since, newest first (ties by
// article ID). A zero since returns them all.
func (t *tenantStore) Hits(ctx context.Context, watchID string, since time.Time) ([]watch.Hit, error) {
	h, err := t.handle()
	if err != nil {
		return nil, err
	}
	from := int64(math.MinInt64)
	if !since.IsZero() {
		from = ms(since)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT article_id, at_ms, alerts FROM hits
		WHERE watch_id = ? AND at_ms >= ? ORDER BY at_ms DESC, article_id ASC`, watchID, from)
	if err != nil {
		return nil, fmt.Errorf("store: hits: %w", err)
	}
	defer rows.Close()
	var out []watch.Hit
	for rows.Next() {
		hit := watch.Hit{WatchID: watchID}
		var at int64
		var alerts string
		if err := rows.Scan(&hit.ArticleID, &at, &alerts); err != nil {
			return nil, fmt.Errorf("store: hits: %w", err)
		}
		hit.At = fromMS(at)
		if hit.Alerts, err = decodeList(alerts); err != nil {
			return nil, fmt.Errorf("store: hits: %w", err)
		}
		out = append(out, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: hits: %w", err)
	}
	return out, nil
}

// Unread counts the watch's hits newer than its read marker. A watch that was
// never marked has the marker 0 (the unix epoch), so all its hits count.
func (t *tenantStore) Unread(ctx context.Context, watchID string) (int, error) {
	h, err := t.handle()
	if err != nil {
		return 0, err
	}
	var n int
	err = h.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM hits WHERE watch_id = ?
		AND at_ms > COALESCE((SELECT at_ms FROM seen WHERE watch_id = ?), 0)`, watchID, watchID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: unread: %w", err)
	}
	return n, nil
}

// MarkSeen moves the watch's read marker to tm. The marker only moves
// forward: an earlier time than the current marker changes nothing.
func (t *tenantStore) MarkSeen(ctx context.Context, watchID string, tm time.Time) error {
	h, err := t.handle()
	if err != nil {
		return err
	}
	if watchID == "" {
		return errors.New("store: mark seen: empty watch ID")
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO seen (watch_id, at_ms) VALUES (?, ?)
		ON CONFLICT (watch_id) DO UPDATE SET at_ms = MAX(seen.at_ms, excluded.at_ms)`, watchID, ms(tm))
	if err != nil {
		return fmt.Errorf("store: mark seen: %w", err)
	}
	return nil
}

// tenantLabels is the labels.Store of one tenant.
type tenantLabels struct{ t *tenantStore }

var _ labels.Store = tenantLabels{}

// Put appends a label to the history. An empty TenantID is filled in; a
// different tenant's ID is an error. A zero At is stored as now.
func (l tenantLabels) Put(ctx context.Context, lb labels.Label) error {
	h, err := l.t.handle()
	if err != nil {
		return err
	}
	if lb.TenantID != "" && lb.TenantID != l.t.id {
		return fmt.Errorf("store: put label: label of tenant %q written to tenant %q", lb.TenantID, l.t.id)
	}
	if lb.ArticleID == "" {
		return errors.New("store: put label: empty article ID")
	}
	if lb.Verdict != labels.Keep && lb.Verdict != labels.Drop {
		return fmt.Errorf("store: put label: unknown verdict %q", lb.Verdict)
	}
	at := lb.At
	if at.IsZero() {
		at = l.t.db.now()
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO labels (article_id, verdict, at_ms, lens_version, sampled, tenant_id)
		VALUES (?, ?, ?, ?, ?, ?)`, lb.ArticleID, string(lb.Verdict), ms(at), lb.LensVersion, lb.Sampled, l.t.id)
	if err != nil {
		return fmt.Errorf("store: put label: %w", err)
	}
	return nil
}

// All returns the latest label of every article, newest first. Older labels
// stay in the history but are not returned.
func (l tenantLabels) All(ctx context.Context) ([]labels.Label, error) {
	h, err := l.t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT l.article_id, l.verdict, l.at_ms, l.lens_version, l.sampled, l.tenant_id
		FROM labels l
		WHERE l.seq = (SELECT l2.seq FROM labels l2 WHERE l2.article_id = l.article_id
			ORDER BY l2.at_ms DESC, l2.seq DESC LIMIT 1)
		ORDER BY l.at_ms DESC, l.seq DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: labels: %w", err)
	}
	defer rows.Close()
	var out []labels.Label
	for rows.Next() {
		var lb labels.Label
		var verdict string
		var at int64
		var sampled sql.NullBool
		if err := rows.Scan(&lb.ArticleID, &verdict, &at, &lb.LensVersion, &sampled, &lb.TenantID); err != nil {
			return nil, fmt.Errorf("store: labels: %w", err)
		}
		lb.Verdict, lb.At, lb.Sampled = labels.Verdict(verdict), fromMS(at), sampled.Bool
		out = append(out, lb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: labels: %w", err)
	}
	return out, nil
}
