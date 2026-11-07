package store

import (
	"context"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/model"
)

var _ insider.Store = (*DB)(nil)

// insiderKeep is how long the trades of a filing are kept.
const insiderKeep = 400 * 24 * time.Hour

// UnreadForm4 implements insider.Store: the Form 4 articles (not the 4/A, which
// restates a trade already read) that have no insider_filing row.
func (db *DB) UnreadForm4(ctx context.Context, since time.Time, limit int) ([]insider.Filing, int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, 0, err
	}
	const where = `FROM articles a WHERE a.form = '4' AND a.cik <> '' AND a.published_ms >= ?
		AND NOT EXISTS (SELECT 1 FROM insider_filing f WHERE f.accession = a.id)`
	var total int
	if err := h.r.QueryRowContext(ctx, `SELECT COUNT(*) `+where, ms(since)).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: insiders: %w", err)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT a.id, a.cik, a.published_ms `+where+` ORDER BY a.published_ms DESC, a.id LIMIT ?`, ms(since), limit)
	if err != nil {
		return nil, 0, fmt.Errorf("store: insiders: %w", err)
	}
	defer rows.Close()
	var out []insider.Filing
	for rows.Next() {
		var f insider.Filing
		var at int64
		if err := rows.Scan(&f.Accession, &f.CIK, &at); err != nil {
			return nil, 0, fmt.Errorf("store: insiders: %w", err)
		}
		f.Filed = fromMS(at).Format(time.DateOnly)
		out = append(out, f)
	}
	return out, total, rows.Err()
}

// PutInsider implements insider.Store. Rows older than insiderKeep are dropped
// with each write, so the tables keep to a window.
func (db *DB) PutInsider(ctx context.Context, f insider.Filing, trades []model.InsiderTrade, problem string, at time.Time) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: insiders: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM insider_trade WHERE accession = ?`, f.Accession); err != nil {
		return fmt.Errorf("store: insiders: %w", err)
	}
	for _, t := range trades {
		if _, err := tx.ExecContext(ctx, `INSERT INTO insider_trade
			(accession, seq, cik, owner, owner_cik, role, traded, filed, code, acquired, shares, price, held, direct, plan)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.Accession, t.Seq, f.CIK, t.Owner, t.OwnerCIK, t.Role, t.Date, f.Filed, t.Code, b2i(t.Acquired),
			t.Shares, t.Price, t.After, b2i(t.Direct), b2i(t.Plan)); err != nil {
			return fmt.Errorf("store: insiders: %w", err)
		}
	}
	status := "ok"
	if problem != "" {
		status = "bad"
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO insider_filing (accession, cik, filed, read_ms, trades, status, error)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, f.Accession, f.CIK, f.Filed, ms(at), len(trades), status, problem); err != nil {
		return fmt.Errorf("store: insiders: %w", err)
	}
	cut := at.Add(-insiderKeep).Format(time.DateOnly)
	if _, err := tx.ExecContext(ctx, `DELETE FROM insider_trade WHERE filed < ?`, cut); err != nil {
		return fmt.Errorf("store: insiders: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM insider_filing WHERE filed < ?`, cut); err != nil {
		return fmt.Errorf("store: insiders: %w", err)
	}
	return tx.Commit()
}

// InsiderTrades lists the stored trades whose day is not before since
// (YYYY-MM-DD), newest first; cik, when not empty, limits them to one company,
// and limit, when positive, to that many.
func (db *DB) InsiderTrades(ctx context.Context, since, cik string, limit int) ([]model.InsiderTrade, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	q := `SELECT accession, seq, cik, owner, owner_cik, role, traded, filed, code, acquired, shares, price, held, direct, plan
		FROM insider_trade WHERE traded >= ?`
	args := []any{since}
	if cik != "" {
		q += ` AND cik = ?`
		args = append(args, cik)
	}
	q += ` ORDER BY traded DESC, accession DESC, seq`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := h.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: insiders: %w", err)
	}
	defer rows.Close()
	var out []model.InsiderTrade
	for rows.Next() {
		var t model.InsiderTrade
		var acquired, direct, plan int
		if err := rows.Scan(&t.Accession, &t.Seq, &t.CIK, &t.Owner, &t.OwnerCIK, &t.Role, &t.Date, &t.Filed, &t.Code,
			&acquired, &t.Shares, &t.Price, &t.After, &direct, &plan); err != nil {
			return nil, fmt.Errorf("store: insiders: %w", err)
		}
		t.Acquired, t.Direct, t.Plan = acquired == 1, direct == 1, plan == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
