package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/quota"
)

var _ quota.Store = (*DB)(nil)

func quotaCeil(t time.Time) time.Time {
	rounded := t.Truncate(time.Millisecond)
	if rounded.Before(t) {
		rounded = rounded.Add(time.Millisecond)
	}
	return rounded
}

func scanQuota(row interface{ Scan(...any) error }) (quota.State, error) {
	var s quota.State
	var reset, until int64
	err := row.Scan(&s.Provider, &s.Used, &s.Limit.Requests, &s.Limit.Period, &reset, &until, &s.Status)
	if reset != 0 {
		s.Reset = fromMS(reset)
	}
	if until != 0 {
		s.Until = fromMS(until)
	}
	return s, err
}

// TakeQuota holds SQLite's immediate write transaction through check and reserve.
// Failure to read/write accounting refuses the request rather than bypassing it.
func (db *DB) TakeQuota(ctx context.Context, key string, limit quota.Limit, now time.Time) error {
	if !quota.ValidKey(key) || now.IsZero() {
		return fmt.Errorf("quota: invalid provider or clock")
	}
	if err := limit.Validate(); err != nil {
		return err
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, err := scanQuota(tx.QueryRowContext(ctx, `SELECT provider,used,requests,period_ns,reset_ms,until_ms,status FROM provider_quota WHERE provider=?`, key))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	s.Provider = key
	s = s.WithLimit(limit, now)
	blocked := s.Check(now)
	if limit.Requests == 0 {
		return blocked // no invented usage/remaining balance for unknown quotas
	}
	if blocked == nil {
		if s.Reset.IsZero() {
			s.Reset = quotaCeil(now.Add(limit.Period))
		}
		s.Used++
	}
	// Persist configuration/window transitions even when this attempt is refused.
	var resetMS int64
	if !s.Reset.IsZero() {
		resetMS = ms(s.Reset)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO provider_quota(provider,used,requests,period_ns,reset_ms) VALUES(?,?,?,?,?)
		ON CONFLICT(provider) DO UPDATE SET used=excluded.used,requests=excluded.requests,period_ns=excluded.period_ns,reset_ms=excluded.reset_ms`,
		key, s.Used, limit.Requests, int64(limit.Period), resetMS)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return blocked
}

// CheckQuotaCooldown revalidates a reserved attempt after rate-limiter waiting.
// Its budget slot is already charged, so only a newly observed throttle applies.
func (db *DB) CheckQuotaCooldown(ctx context.Context, key string, now time.Time) error {
	if !quota.ValidKey(key) || now.IsZero() {
		return fmt.Errorf("quota: invalid provider or clock")
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	s, err := scanQuota(h.r.QueryRowContext(ctx, `SELECT provider,used,requests,period_ns,reset_ms,until_ms,status FROM provider_quota WHERE provider=?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	s.Limit = quota.Limit{}
	return s.Check(now)
}

func (db *DB) BlockQuota(ctx context.Context, key string, until time.Time, status int) error {
	if !quota.ValidKey(key) || until.IsZero() {
		return fmt.Errorf("quota: invalid provider or retry time")
	}
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO provider_quota(provider,until_ms,status) VALUES(?,?,?)
		ON CONFLICT(provider) DO UPDATE SET until_ms=CASE WHEN provider_quota.until_ms >= excluded.until_ms THEN provider_quota.until_ms ELSE excluded.until_ms END,status=excluded.status`, key, ms(quotaCeil(until)), status)
	return err
}

func (db *DB) QuotaStates(ctx context.Context) ([]quota.State, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT provider,used,requests,period_ns,reset_ms,until_ms,status FROM provider_quota ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []quota.State{}
	for rows.Next() {
		s, err := scanQuota(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
