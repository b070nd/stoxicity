package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

// nullable stores an empty string as NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PutSymbolHistory appends a revision for each span that is new, or that differs
// from the latest stored revision of the same security and first day, and
// reports how many revisions were written. A span is stored as
// SymbolSpan.Normalized gives it; one that does not Validate is an error and
// nothing is written. A correction is the same security and first day with new
// content. All in one transaction; at is the recorded time, the store's clock
// when zero.
func (db *DB) PutSymbolHistory(ctx context.Context, rows []marketdata.SymbolSpan, at time.Time) (int, error) {
	spans := make([]marketdata.SymbolSpan, len(rows))
	for i, r := range rows {
		spans[i] = r.Normalized()
		if err := spans[i].Validate(); err != nil {
			return 0, fmt.Errorf("store: symbol history: %w", err)
		}
	}
	if len(spans) == 0 {
		return 0, nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	if at.IsZero() {
		at = db.now()
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: symbol history: %w", err)
	}
	defer tx.Rollback()
	latest, err := tx.PrepareContext(ctx, `SELECT revision, symbol, valid_to, kind, successor, source, source_ref, verified, note
		FROM security_symbol WHERE security = ? AND valid_from = ? ORDER BY revision DESC LIMIT 1`)
	if err != nil {
		return 0, fmt.Errorf("store: symbol history: %w", err)
	}
	defer latest.Close()
	ins, err := tx.PrepareContext(ctx, `INSERT INTO security_symbol (security, valid_from, revision, symbol, valid_to, kind, successor,
		source, source_ref, verified, recorded_ms, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("store: symbol history: %w", err)
	}
	defer ins.Close()
	written := 0
	for _, s := range spans {
		var rev int
		var last marketdata.SymbolSpan
		var to, successor sql.NullString
		var kind string
		var verified int
		switch err := latest.QueryRowContext(ctx, s.Security, s.From).Scan(&rev, &last.Symbol, &to, &kind, &successor,
			&last.Source, &last.SourceRef, &verified, &last.Note); err {
		case nil:
			last.Security, last.From, last.To, last.Successor = s.Security, s.From, to.String, successor.String
			last.Kind, last.Verified = marketdata.SpanKind(kind), verified == 1
			same := s
			same.RecordedAt = time.Time{}
			if last == same {
				continue
			}
		case sql.ErrNoRows:
		default:
			return 0, fmt.Errorf("store: symbol history: %w", err)
		}
		v := 0
		if s.Verified {
			v = 1
		}
		if _, err := ins.ExecContext(ctx, s.Security, s.From, rev+1, s.Symbol, nullable(s.To), string(s.Kind), nullable(s.Successor),
			s.Source, s.SourceRef, v, ms(at), s.Note); err != nil {
			return 0, fmt.Errorf("store: symbol history: %w", err)
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: symbol history: %w", err)
	}
	return written, nil
}

// SymbolHistory returns the latest revision of each span, ordered by security
// and first day. A zero asOf reads the history as it stands; otherwise only
// revisions recorded by that time count, so the history known then comes back.
// A span with a verified revision later corrected to unverified comes back
// unverified.
func (db *DB) SymbolHistory(ctx context.Context, asOf time.Time) ([]marketdata.SymbolSpan, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !asOf.IsZero() {
		bound = ms(asOf)
	}
	rows, err := h.r.QueryContext(ctx, `WITH w AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY security, valid_from ORDER BY revision DESC) AS rn
			FROM security_symbol WHERE recorded_ms <= ?)
		SELECT security, symbol, valid_from, valid_to, kind, successor, source, source_ref, verified, recorded_ms, note
		FROM w WHERE rn = 1 ORDER BY security, valid_from`, bound)
	if err != nil {
		return nil, fmt.Errorf("store: symbol history: %w", err)
	}
	defer rows.Close()
	var out []marketdata.SymbolSpan
	for rows.Next() {
		var s marketdata.SymbolSpan
		var to, successor sql.NullString
		var kind string
		var verified int
		var recorded int64
		if err := rows.Scan(&s.Security, &s.Symbol, &s.From, &to, &kind, &successor, &s.Source, &s.SourceRef, &verified, &recorded, &s.Note); err != nil {
			return nil, fmt.Errorf("store: symbol history: %w", err)
		}
		s.To, s.Successor, s.Kind, s.Verified, s.RecordedAt = to.String, successor.String, marketdata.SpanKind(kind), verified == 1, fromMS(recorded)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: symbol history: %w", err)
	}
	return out, nil
}
