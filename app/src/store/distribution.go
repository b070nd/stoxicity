package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/money"
)

// PutDistributions appends payout observations. An observation identical in
// every stated field to the newest one from the same source for the same payout
// adds nothing; any difference (a cut, a cancellation, a moved ex-date) is a
// revision and is kept beside the old one. All rows are validated first and any
// failure stores none.
func (db *DB) PutDistributions(ctx context.Context, obs []distribution.Obs, at time.Time) (ObsCounts, error) {
	var c ObsCounts
	for _, o := range obs {
		if err := o.Validate(); err != nil {
			return c, err
		}
	}
	h, err := db.baseHandle()
	if err != nil {
		return c, err
	}
	if at.IsZero() {
		at = db.now()
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return c, fmt.Errorf("store: distributions: %w", err)
	}
	defer tx.Rollback()
	for _, o := range obs {
		rec := o.Received
		if rec.IsZero() {
			rec = at
		}
		same := func(a []any) string { return fmt.Sprint(a...) }
		var prev []any
		var amount, status, currency, ex, record, pay, declared string
		err := tx.QueryRowContext(ctx, `SELECT status, amount, currency, ex_day, record_day, pay_day, declared_day FROM distribution_obs
			WHERE security_id = ? AND type = ? AND period = ? AND source = ? ORDER BY seq DESC LIMIT 1`,
			o.SecurityID, string(o.Type), o.Period, o.Source).Scan(&status, &amount, &currency, &ex, &record, &pay, &declared)
		switch {
		case err == nil:
			prev = []any{status, amount, currency, ex, record, pay, declared}
			now := []any{string(o.Status), o.Amount.String(), string(o.Currency), o.ExDay, o.RecordDay, o.PayDay, o.DeclaredDay}
			if same(prev) == same(now) {
				c.Unchanged++
				continue
			}
		case err != sql.ErrNoRows:
			return c, fmt.Errorf("store: distributions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO distribution_obs (security_id, type, period, status, amount, currency, ex_day, record_day, pay_day, declared_day, source, source_ref, received_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			o.SecurityID, string(o.Type), o.Period, string(o.Status), o.Amount.String(), string(o.Currency), o.ExDay, o.RecordDay, o.PayDay, o.DeclaredDay, o.Source, o.SourceRef, ms(rec)); err != nil {
			return c, fmt.Errorf("store: distributions: %w", err)
		}
		c.New++
	}
	if err := tx.Commit(); err != nil {
		return c, fmt.Errorf("store: distributions: %w", err)
	}
	return c, nil
}

// DistributionObservations returns the observations of the given securities (all
// when none are given) received by knownAt (zero: all), oldest first.
func (db *DB) DistributionObservations(ctx context.Context, securityIDs []string, knownAt time.Time) ([]distribution.Obs, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !knownAt.IsZero() {
		bound = ms(knownAt)
	}
	q := `SELECT seq, security_id, type, period, status, amount, currency, ex_day, record_day, pay_day, declared_day, source, source_ref, received_ms
		FROM distribution_obs WHERE received_ms <= ?`
	args := []any{bound}
	if len(securityIDs) > 0 {
		q += ` AND security_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(securityIDs)), ",") + `)`
		for _, id := range securityIDs {
			args = append(args, id)
		}
	}
	rows, err := h.r.QueryContext(ctx, q+` ORDER BY ex_day, seq`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: distributions: %w", err)
	}
	defer rows.Close()
	var out []distribution.Obs
	for rows.Next() {
		var o distribution.Obs
		var typ, st, amt, cur string
		var rec int64
		if err := rows.Scan(&o.Seq, &o.SecurityID, &typ, &o.Period, &st, &amt, &cur, &o.ExDay, &o.RecordDay, &o.PayDay, &o.DeclaredDay, &o.Source, &o.SourceRef, &rec); err != nil {
			return nil, fmt.Errorf("store: distributions: %w", err)
		}
		v, err := money.ParseDecimal(amt)
		if err != nil {
			return nil, fmt.Errorf("store: distributions: stored amount %q: %w", amt, err)
		}
		o.Type, o.Status, o.Amount, o.Currency, o.Received = distribution.Type(typ), distribution.Status(st), v, money.Currency(cur), time.UnixMilli(rec).UTC()
		out = append(out, o)
	}
	return out, rows.Err()
}
