package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
	"github.com/b070nd/stoxicity/app/src/valuation"
)

var (
	_ fundamentals.CoverStore = (*DB)(nil)
	_ valuation.Source        = (*DB)(nil)
)

// PutCover stores the share counts of a report. A count already stored is the
// same row; the same count stated twice by the report (two contexts of one
// class) is one row that says how many facts it stands for; a different count
// for the same class and date is a row of its own, so that a conflict or an
// amendment stays visible. Nothing is overwritten. The issuer is noted as read.
func (db *DB) PutCover(ctx context.Context, r fundamentals.CoverRead) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	at := r.At
	if at.IsZero() {
		at = db.now()
	}
	type key struct {
		dims, asOf string
		shares     float64
	}
	type val struct {
		ticker string
		n      int
	}
	merged := map[key]*val{}
	var order []key
	for _, c := range r.Classes {
		k := key{c.Dimensions, c.AsOf, c.Shares}
		if v, ok := merged[k]; ok {
			v.n++
			if v.ticker == "" {
				v.ticker = c.Ticker
			}
			continue
		}
		merged[k] = &val{ticker: c.Ticker, n: 1}
		order = append(order, k)
	}
	precision, accepted := "day", int64(0)
	if !r.Accepted.IsZero() {
		precision, accepted = "time", ms(r.Accepted)
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return fmt.Errorf("store: cover: %w", err)
	}
	defer tx.Rollback()
	for _, k := range order {
		v := merged[k]
		if _, err := tx.ExecContext(ctx, `INSERT INTO share_class_obs
			(cik, accession, form, filed_day, accepted_ms, precision, as_of, dimensions, shares, ticker, duplicates, source, received_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'sec-cover', ?) ON CONFLICT DO NOTHING`,
			r.CIK, r.Accession, r.Form, r.Filed, accepted, precision, k.asOf, k.dims, k.shares, v.ticker, v.n, ms(at)); err != nil {
			return fmt.Errorf("store: cover: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cover_check (cik, checked_ms, accession, problem) VALUES (?, ?, ?, '')
		ON CONFLICT (cik) DO UPDATE SET checked_ms = excluded.checked_ms, accession = excluded.accession, problem = '',
		kind = '', failures = 0, next_ms = 0`,
		r.CIK, ms(at), r.Accession); err != nil {
		return fmt.Errorf("store: cover: %w", err)
	}
	return tx.Commit()
}

// PutCoverCheck notes an issuer as looked at, with its retry state. An empty
// accession leaves the stored one.
func (db *DB) PutCoverCheck(ctx context.Context, cik string, c fundamentals.CoverCheck) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	var next int64
	if !c.Next.IsZero() {
		next = ms(c.Next)
	}
	if _, err := h.w.ExecContext(ctx, `INSERT INTO cover_check (cik, checked_ms, accession, problem, kind, failures, next_ms) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (cik) DO UPDATE SET checked_ms = excluded.checked_ms, problem = excluded.problem, kind = excluded.kind,
		failures = excluded.failures, next_ms = excluded.next_ms,
		accession = CASE WHEN excluded.accession <> '' THEN excluded.accession ELSE cover_check.accession END`,
		cik, ms(c.At), c.Accession, truncate(c.Problem, 300), c.Kind, c.Failures, next); err != nil {
		return fmt.Errorf("store: cover check: %w", err)
	}
	return nil
}

// CoverChecks implements fundamentals.CoverStore.
func (db *DB) CoverChecks(ctx context.Context) (map[string]fundamentals.CoverCheck, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT cik, checked_ms, accession, problem, kind, failures, next_ms FROM cover_check`)
	if err != nil {
		return nil, fmt.Errorf("store: cover checks: %w", err)
	}
	defer rows.Close()
	out := map[string]fundamentals.CoverCheck{}
	for rows.Next() {
		var cik string
		var c fundamentals.CoverCheck
		var at, next int64
		if err := rows.Scan(&cik, &at, &c.Accession, &c.Problem, &c.Kind, &c.Failures, &next); err != nil {
			return nil, fmt.Errorf("store: cover checks: %w", err)
		}
		c.At = fromMS(at)
		if next > 0 {
			c.Next = fromMS(next)
		}
		out[cik] = c
	}
	return out, rows.Err()
}

// ClassObs implements valuation.Source: every stored share count.
func (db *DB) ClassObs(ctx context.Context) ([]valuation.Obs, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT cik, accession, form, filed_day, accepted_ms, as_of, dimensions, shares, ticker, duplicates, received_ms
		FROM share_class_obs ORDER BY cik, seq`)
	if err != nil {
		return nil, fmt.Errorf("store: class observations: %w", err)
	}
	defer rows.Close()
	var out []valuation.Obs
	for rows.Next() {
		var o valuation.Obs
		var acc, rec int64
		if err := rows.Scan(&o.CIK, &o.Accession, &o.Form, &o.Filed, &acc, &o.AsOf, &o.Dimensions, &o.Shares, &o.Ticker, &o.Duplicates, &rec); err != nil {
			return nil, fmt.Errorf("store: class observations: %w", err)
		}
		if acc > 0 {
			o.Accepted = fromMS(acc)
		}
		o.Received = fromMS(rec)
		out = append(out, o)
	}
	return out, rows.Err()
}

// PutClassMaps appends the mappings that differ from the latest one of their
// class (in status, security, ticker or method) and returns how many it
// appended. A mapping that says the same again is not stored again, so the
// history shows changes only.
func (db *DB) PutClassMaps(ctx context.Context, maps []valuation.Mapping) (int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: class maps: %w", err)
	}
	defer tx.Rollback()
	n := 0
	for _, m := range maps {
		var status, sec, ticker, method string
		err := tx.QueryRowContext(ctx, `SELECT status, security_id, ticker, method FROM share_class_map
			WHERE cik = ? AND dimensions = ? ORDER BY seq DESC LIMIT 1`, m.CIK, m.Dimensions).Scan(&status, &sec, &ticker, &method)
		if err == nil && status == m.Status && sec == m.SecurityID && ticker == m.Ticker && method == m.Method {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("store: class maps: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO share_class_map (cik, dimensions, security_id, ticker, status, method, confidence, basis, accession, mapped_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.CIK, m.Dimensions, m.SecurityID, m.Ticker, m.Status, m.Method, m.Confidence, m.Basis, m.Accession, ms(m.MappedAt)); err != nil {
			return 0, fmt.Errorf("store: class maps: %w", err)
		}
		n++
	}
	return n, tx.Commit()
}

// ClassMaps implements valuation.Source: the latest mapping of each class made
// by the time (zero: now).
func (db *DB) ClassMaps(ctx context.Context, at time.Time) (map[string]map[string]valuation.Mapping, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	limit := int64(1) << 62
	if !at.IsZero() {
		limit = ms(at)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT m.cik, m.dimensions, m.security_id, m.ticker, m.status, m.method, m.confidence, m.basis, m.accession, m.mapped_ms
		FROM share_class_map m WHERE m.seq = (SELECT MAX(m2.seq) FROM share_class_map m2
			WHERE m2.cik = m.cik AND m2.dimensions = m.dimensions AND m2.mapped_ms <= ?)`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: class maps: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]valuation.Mapping{}
	for rows.Next() {
		var m valuation.Mapping
		var when int64
		if err := rows.Scan(&m.CIK, &m.Dimensions, &m.SecurityID, &m.Ticker, &m.Status, &m.Method, &m.Confidence, &m.Basis, &m.Accession, &when); err != nil {
			return nil, fmt.Errorf("store: class maps: %w", err)
		}
		m.MappedAt = fromMS(when)
		if out[m.CIK] == nil {
			out[m.CIK] = map[string]valuation.Mapping{}
		}
		out[m.CIK][m.Dimensions] = m
	}
	return out, rows.Err()
}

// ClassMapHistory returns every mapping ever made for the issuer, oldest first.
func (db *DB) ClassMapHistory(ctx context.Context, cik string) ([]valuation.Mapping, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT cik, dimensions, security_id, ticker, status, method, confidence, basis, accession, mapped_ms
		FROM share_class_map WHERE cik = ? ORDER BY seq`, cik)
	if err != nil {
		return nil, fmt.Errorf("store: class maps: %w", err)
	}
	defer rows.Close()
	var out []valuation.Mapping
	for rows.Next() {
		var m valuation.Mapping
		var when int64
		if err := rows.Scan(&m.CIK, &m.Dimensions, &m.SecurityID, &m.Ticker, &m.Status, &m.Method, &m.Confidence, &m.Basis, &m.Accession, &when); err != nil {
			return nil, fmt.Errorf("store: class maps: %w", err)
		}
		m.MappedAt = fromMS(when)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SecurityOf returns the stable security that carries the ticker, creating it
// when there is none yet.
func (db *DB) SecurityOf(ctx context.Context, ticker string, at time.Time) (string, error) {
	h, err := db.baseHandle()
	if err != nil {
		return "", err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return "", fmt.Errorf("store: security: %w", err)
	}
	defer tx.Rollback()
	id, err := ensureSecurity(ctx, tx, ticker, at)
	if err != nil {
		return "", fmt.Errorf("store: security: %w", err)
	}
	return id, tx.Commit()
}

// MapClasses maps the classes of the issuer's newest stored count (as known at
// the time) to the listings, and stores the mappings that are new or changed.
func (db *DB) MapClasses(ctx context.Context, cik string, listings []valuation.Listing, at time.Time) (int, error) {
	obs, err := db.ClassObs(ctx)
	if err != nil {
		return 0, err
	}
	var mine []valuation.Obs
	for _, o := range obs {
		if o.CIK == cik {
			mine = append(mine, o)
		}
	}
	pick, ok := valuation.Select(mine, at)
	if !ok {
		return 0, nil
	}
	return db.PutClassMaps(ctx, valuation.MapClasses(pick, listings, at))
}
