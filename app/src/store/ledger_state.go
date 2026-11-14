package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The methods in this file keep the state of the hit ledger in the tenant's
// database: evaluation cursors, delivery records and sent digests.

// Cursor returns the watch's evaluation cursor.
func (t *tenantStore) Cursor(ctx context.Context, watchID string) (Cursor, bool, error) {
	h, err := t.handle()
	if err != nil {
		return Cursor{}, false, err
	}
	var c Cursor
	var fetched int64
	err = h.r.QueryRowContext(ctx, `SELECT fetched_ms, watch_hash, seq FROM eval_cursor WHERE watch_id = ?`, watchID).
		Scan(&fetched, &c.Hash, &c.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, false, nil
	}
	if err != nil {
		return Cursor{}, false, fmt.Errorf("store: cursor: %w", err)
	}
	c.Fetched = fromMS(fetched)
	return c, true, nil
}

// SetCursor stores the watch's evaluation cursor.
func (t *tenantStore) SetCursor(ctx context.Context, watchID string, c Cursor) error {
	h, err := t.handle()
	if err != nil {
		return err
	}
	if watchID == "" {
		return errors.New("store: set cursor: empty watch ID")
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO eval_cursor (watch_id, fetched_ms, watch_hash, seq) VALUES (?, ?, ?, ?)
		ON CONFLICT (watch_id) DO UPDATE SET fetched_ms = excluded.fetched_ms, watch_hash = excluded.watch_hash, seq = excluded.seq`,
		watchID, ms(c.Fetched), c.Hash, c.Seq)
	if err != nil {
		return fmt.Errorf("store: set cursor: %w", err)
	}
	return nil
}

// PendingAlerts returns the alert names of hits that were not delivered.
func (t *tenantStore) PendingAlerts(ctx context.Context) ([]PendingAlert, error) {
	h, err := t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT h.watch_id, h.article_id, a.value, h.at_ms
		FROM hits h CROSS JOIN `+h.d.jsonEach("h.alerts", "a")+`
		LEFT JOIN delivered d ON d.watch_id = h.watch_id AND d.article_id = h.article_id AND d.alert = a.value
		WHERE h.alerts <> '[]' AND d.alert IS NULL
		ORDER BY h.at_ms, h.watch_id, h.article_id, a.value`)
	if err != nil {
		return nil, fmt.Errorf("store: pending alerts: %w", err)
	}
	defer rows.Close()
	var out []PendingAlert
	for rows.Next() {
		var p PendingAlert
		var at int64
		if err := rows.Scan(&p.WatchID, &p.ArticleID, &p.Alert, &at); err != nil {
			return nil, fmt.Errorf("store: pending alerts: %w", err)
		}
		p.At = fromMS(at)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: pending alerts: %w", err)
	}
	return out, nil
}

// MarkDelivered records the alerts as delivered at the given time, in one
// transaction. A zero time is stored as now.
func (t *tenantStore) MarkDelivered(ctx context.Context, alerts []PendingAlert, at time.Time) error {
	h, err := t.handle()
	if err != nil {
		return err
	}
	if len(alerts) == 0 {
		return nil
	}
	if at.IsZero() {
		at = t.db.now()
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: mark delivered: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO delivered (watch_id, article_id, alert, at_ms) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("store: mark delivered: %w", err)
	}
	defer stmt.Close()
	for _, p := range alerts {
		if p.WatchID == "" || p.ArticleID == "" || p.Alert == "" {
			return errors.New("store: mark delivered: alert without watch, article or name")
		}
		if _, err := stmt.ExecContext(ctx, p.WatchID, p.ArticleID, p.Alert, ms(at)); err != nil {
			return fmt.Errorf("store: mark delivered: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: mark delivered: %w", err)
	}
	return nil
}

// LastDelivery returns the time of the latest delivery of the alert.
func (t *tenantStore) LastDelivery(ctx context.Context, watchID, alert string) (time.Time, bool, error) {
	h, err := t.handle()
	if err != nil {
		return time.Time{}, false, err
	}
	var last sql.NullInt64
	err = h.r.QueryRowContext(ctx, `SELECT MAX(at_ms) FROM delivered WHERE watch_id = ? AND alert = ?`, watchID, alert).Scan(&last)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: last delivery: %w", err)
	}
	if !last.Valid {
		return time.Time{}, false, nil
	}
	return fromMS(last.Int64), true, nil
}

// DigestSent reports whether the digest of the period was sent.
func (t *tenantStore) DigestSent(ctx context.Context, watchID, periodKey string) (bool, error) {
	h, err := t.handle()
	if err != nil {
		return false, err
	}
	var n int
	err = h.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM digest_sent WHERE watch_id = ? AND period_key = ?`, watchID, periodKey).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: digest sent: %w", err)
	}
	return n > 0, nil
}

// MarkDigestSent records that the digest of the period was sent. A zero time is
// stored as now.
func (t *tenantStore) MarkDigestSent(ctx context.Context, watchID, periodKey string, at time.Time) error {
	h, err := t.handle()
	if err != nil {
		return err
	}
	if watchID == "" || periodKey == "" {
		return errors.New("store: mark digest sent: empty watch ID or period")
	}
	if at.IsZero() {
		at = t.db.now()
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO digest_sent (watch_id, period_key, at_ms) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		watchID, periodKey, ms(at))
	if err != nil {
		return fmt.Errorf("store: mark digest sent: %w", err)
	}
	return nil
}

// DeliveredChannels returns, for each article ID, the channels the alert of the
// watch was already sent to.
func (t *tenantStore) DeliveredChannels(ctx context.Context, watchID, alert string) (map[string]map[string]bool, error) {
	h, err := t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT article_id, channel FROM delivered_channel WHERE watch_id = ? AND alert = ?`, watchID, alert)
	if err != nil {
		return nil, fmt.Errorf("store: delivered channels: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var article, ch string
		if err := rows.Scan(&article, &ch); err != nil {
			return nil, fmt.Errorf("store: delivered channels: %w", err)
		}
		if out[article] == nil {
			out[article] = map[string]bool{}
		}
		out[article][ch] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: delivered channels: %w", err)
	}
	return out, nil
}

// MarkSentTo records the alerts as sent to the channel, in one transaction. A
// zero time is stored as now.
func (t *tenantStore) MarkSentTo(ctx context.Context, alerts []PendingAlert, channel string, at time.Time) error {
	h, err := t.handle()
	if err != nil {
		return err
	}
	if len(alerts) == 0 {
		return nil
	}
	if channel == "" {
		return errors.New("store: mark sent: empty channel")
	}
	if at.IsZero() {
		at = t.db.now()
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: mark sent: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO delivered_channel (watch_id, article_id, alert, channel, at_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("store: mark sent: %w", err)
	}
	defer stmt.Close()
	for _, p := range alerts {
		if p.WatchID == "" || p.ArticleID == "" || p.Alert == "" {
			return errors.New("store: mark sent: alert without watch, article or name")
		}
		if _, err := stmt.ExecContext(ctx, p.WatchID, p.ArticleID, p.Alert, channel, ms(at)); err != nil {
			return fmt.Errorf("store: mark sent: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: mark sent: %w", err)
	}
	return nil
}
