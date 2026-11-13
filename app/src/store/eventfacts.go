package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
)

// PutEventFacts appends a revision for each record that is new or whose
// eventfact.Hash differs from its latest stored revision, and reports how many
// revisions were written. A record that does not Validate is an error and
// nothing is written. All in one transaction.
func (db *DB) PutEventFacts(ctx context.Context, records []eventfact.Record, at time.Time) (int, error) {
	for _, r := range records {
		if err := r.Validate(); err != nil {
			return 0, fmt.Errorf("store: event facts: %w", err)
		}
	}
	if len(records) == 0 {
		return 0, nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	if at.IsZero() {
		at = db.now()
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: event facts: %w", err)
	}
	defer tx.Rollback()
	latest, err := tx.PrepareContext(ctx, `SELECT revision, hash, basis FROM event_fact WHERE id = ? ORDER BY revision DESC LIMIT 1`)
	if err != nil {
		return 0, fmt.Errorf("store: event facts: %w", err)
	}
	defer latest.Close()
	ins, err := tx.PrepareContext(ctx, `INSERT INTO event_fact (id, revision, hash, key, category, type, security_id, symbol, cik, event_id,
		published_ms, available_ms, precision, source, source_ref, basis, extractor, version, body, recorded_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("store: event facts: %w", err)
	}
	defer ins.Close()
	written := 0
	for _, r := range records {
		hash := eventfact.Hash(r)
		var rev int
		var last, lastBasis string
		switch err := latest.QueryRowContext(ctx, r.ID).Scan(&rev, &last, &lastBasis); err {
		case nil:
			if last == hash {
				continue
			}
			// That an event was held when it happened cannot be recreated later: a
			// reading of the same content that says "reconstructed" (an older row
			// fetched again, a rebuild) must not replace it.
			if lastBasis == string(eventfact.Known) && r.Basis == eventfact.Reconstructed {
				same := r
				same.Basis = eventfact.Known
				if eventfact.Hash(same) == last {
					continue
				}
			}
		case sql.ErrNoRows:
		default:
			return 0, fmt.Errorf("store: event facts: %w", err)
		}
		body, err := json.Marshal(r)
		if err != nil {
			return 0, fmt.Errorf("store: event facts: %w", err)
		}
		if _, err := ins.ExecContext(ctx, r.ID, rev+1, hash, r.Key, string(r.Category), string(r.Type), r.SecurityID, r.Symbol, r.CIK, r.EventID,
			ms(r.PublishedAt), ms(r.AvailableAt), r.Precision, string(r.Source), r.SourceRef, string(r.Basis), r.Extractor, r.Version,
			string(body), ms(at)); err != nil {
			return 0, fmt.Errorf("store: event facts: %w", err)
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: event facts: %w", err)
	}
	return written, nil
}

// EventFactQuery says which records EventFacts returns.
type EventFactQuery struct {
	From, To time.Time        // by AvailableAt, inclusive; zero means open
	Types    []eventfact.Type // nil: every type
	Symbol   string           // "" : every security
	AsOf     time.Time        // only revisions recorded by this time; zero: as it stands
	Limit    int              // 0: no limit
}

func decodeEventFact(body string) (eventfact.Record, error) {
	var r eventfact.Record
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return r, fmt.Errorf("store: event facts: %w", err)
	}
	r.PublishedAt, r.AvailableAt = r.PublishedAt.UTC(), r.AvailableAt.UTC()
	return r, nil
}

// EventFacts returns the latest revision (as of q.AsOf) of each record that
// matches, ordered by AvailableAt and then ID.
func (db *DB) EventFacts(ctx context.Context, q EventFactQuery) ([]eventfact.Record, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !q.AsOf.IsZero() {
		bound = ms(q.AsOf)
	}
	args := []any{bound}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("?%d", len(args))
	}
	query := `WITH w AS (SELECT id, available_ms, type, symbol, body, ROW_NUMBER() OVER (PARTITION BY id ORDER BY revision DESC) AS rn
		FROM event_fact WHERE recorded_ms <= ?1)
		SELECT body FROM w WHERE rn = 1`
	if !q.From.IsZero() {
		query += " AND available_ms >= " + arg(ms(q.From))
	}
	if !q.To.IsZero() {
		query += " AND available_ms <= " + arg(ms(q.To))
	}
	if q.Symbol != "" {
		query += " AND symbol = " + arg(q.Symbol)
	}
	if len(q.Types) > 0 {
		marks := make([]string, len(q.Types))
		for i, t := range q.Types {
			marks[i] = arg(string(t))
		}
		query += " AND type IN (" + strings.Join(marks, ",") + ")"
	}
	query += " ORDER BY available_ms, id"
	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := h.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: event facts: %w", err)
	}
	defer rows.Close()
	var out []eventfact.Record
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("store: event facts: %w", err)
		}
		r, err := decodeEventFact(body)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: event facts: %w", err)
	}
	return out, nil
}

// EventFactRevision is one stored revision of a record.
type EventFactRevision struct {
	Revision   int
	Hash       string
	RecordedAt time.Time
	Record     eventfact.Record
}

// EventFactHistory returns every revision of one record, oldest first.
func (db *DB) EventFactHistory(ctx context.Context, id string) ([]EventFactRevision, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT revision, hash, recorded_ms, body FROM event_fact WHERE id = ? ORDER BY revision`, id)
	if err != nil {
		return nil, fmt.Errorf("store: event facts: %w", err)
	}
	defer rows.Close()
	var out []EventFactRevision
	for rows.Next() {
		var v EventFactRevision
		var rec int64
		var body string
		if err := rows.Scan(&v.Revision, &v.Hash, &rec, &body); err != nil {
			return nil, fmt.Errorf("store: event facts: %w", err)
		}
		v.RecordedAt = fromMS(rec)
		if v.Record, err = decodeEventFact(body); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: event facts: %w", err)
	}
	return out, nil
}

// EventFactCount is how many records one type has.
type EventFactCount struct {
	Type        eventfact.Type
	Records     int
	Known       int // records whose basis is known
	First, Last time.Time
}

// EventFactCounts returns how many records (latest revision) there are per
// type, with the earliest and latest availability, ordered by type.
func (db *DB) EventFactCounts(ctx context.Context) ([]EventFactCount, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `WITH w AS (SELECT type, basis, available_ms, ROW_NUMBER() OVER (PARTITION BY id ORDER BY revision DESC) AS rn FROM event_fact)
		SELECT type, COUNT(*), SUM(basis = 'known'), MIN(available_ms), MAX(available_ms) FROM w WHERE rn = 1 GROUP BY type ORDER BY type`)
	if err != nil {
		return nil, fmt.Errorf("store: event facts: %w", err)
	}
	defer rows.Close()
	var out []EventFactCount
	for rows.Next() {
		var c EventFactCount
		var typ string
		var first, last int64
		if err := rows.Scan(&typ, &c.Records, &c.Known, &first, &last); err != nil {
			return nil, fmt.Errorf("store: event facts: %w", err)
		}
		c.Type, c.First, c.Last = eventfact.Type(typ), fromMS(first), fromMS(last)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: event facts: %w", err)
	}
	return out, nil
}
