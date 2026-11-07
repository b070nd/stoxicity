package store

import (
	"context"
	"fmt"
	"time"
)

// FilingRef is a filing as an EDGAR index lists it.
type FilingRef struct {
	Accession, CIK, Form, Filed, Company string
}

// IndexDay says what is stored of one day of the index.
type IndexDay struct {
	Day      string
	Read     time.Time
	Listed   int  // filings the index listed that day, all forms
	NotFound bool // the SEC had no index for the day
}

// PutFilingIndex stores the wanted filings of one day and notes that the day was
// read: how many filings it listed in all, and whether the SEC had it. A day
// read again adds the filings not stored yet and changes none.
func (db *DB) PutFilingIndex(ctx context.Context, day string, refs []FilingRef, listed int, notFound bool, at time.Time) (added int, err error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: filing index: %w", err)
	}
	defer tx.Rollback()
	for _, r := range refs {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO filing_index (accession, cik, form, filed_day, company, source, first_seen_ms)
			VALUES (?, ?, ?, ?, ?, 'edgar-daily-index', ?)`, r.Accession, r.CIK, r.Form, r.Filed, r.Company, ms(at))
		if err != nil {
			return 0, fmt.Errorf("store: filing index: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO filing_index_day (day, read_ms, listed, missing) VALUES (?, ?, ?, ?)
		ON CONFLICT (day) DO UPDATE SET read_ms = excluded.read_ms, listed = excluded.listed, missing = excluded.missing`,
		day, ms(at), listed, notFound); err != nil {
		return 0, fmt.Errorf("store: filing index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: filing index: %w", err)
	}
	return added, nil
}

// IndexDays returns the stored days of the index from the day on.
func (db *DB) IndexDays(ctx context.Context, from string) (map[string]IndexDay, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT day, read_ms, listed, missing FROM filing_index_day WHERE day >= ?`, from)
	if err != nil {
		return nil, fmt.Errorf("store: filing index: %w", err)
	}
	defer rows.Close()
	out := map[string]IndexDay{}
	for rows.Next() {
		var d IndexDay
		var read int64
		if err := rows.Scan(&d.Day, &read, &d.Listed, &d.NotFound); err != nil {
			return nil, fmt.Errorf("store: filing index: %w", err)
		}
		d.Read = fromMS(read)
		out[d.Day] = d
	}
	return out, rows.Err()
}

// MissingFilings returns the filings the index lists for the days that no stored
// article has, oldest first.
func (db *DB) MissingFilings(ctx context.Context, from, to string) ([]FilingRef, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT i.accession, i.cik, i.form, i.filed_day, i.company FROM filing_index i
		WHERE i.filed_day >= ? AND i.filed_day <= ? AND NOT EXISTS (SELECT 1 FROM articles a WHERE a.id = i.accession)
		ORDER BY i.filed_day, i.accession`, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: filing index: %w", err)
	}
	defer rows.Close()
	var out []FilingRef
	for rows.Next() {
		var r FilingRef
		if err := rows.Scan(&r.Accession, &r.CIK, &r.Form, &r.Filed, &r.Company); err != nil {
			return nil, fmt.Errorf("store: filing index: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FilingsOnDays counts, for each day, the filings the index lists and how many
// of them have an article.
func (db *DB) FilingsOnDays(ctx context.Context, from, to string) (map[string][2]int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT i.filed_day, COUNT(*), SUM(EXISTS (SELECT 1 FROM articles a WHERE a.id = i.accession))
		FROM filing_index i WHERE i.filed_day >= ? AND i.filed_day <= ? GROUP BY i.filed_day`, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: filing index: %w", err)
	}
	defer rows.Close()
	out := map[string][2]int{}
	for rows.Next() {
		var d string
		var n, have int
		if err := rows.Scan(&d, &n, &have); err != nil {
			return nil, fmt.Errorf("store: filing index: %w", err)
		}
		out[d] = [2]int{n, have}
	}
	return out, rows.Err()
}
