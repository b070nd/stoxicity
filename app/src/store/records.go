package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Record is one immutable row of the tenant's record store.
type Record struct {
	Seq  int64
	Kind string
	Key  string
	Hash string
	Body []byte
	At   time.Time
}

// Records is a tenant's append-only store of typed records. It does not know
// what the bodies mean: the package that owns a kind defines and validates it.
type Records struct{ t tenantStore }

// Records returns the record store of one tenant.
func (db *DB) Records(tenantID string) Records {
	return Records{tenantStore{db: db, id: tenantID, valid: model.ValidID(tenantID)}}
}

var recordKindRE = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,39}$`)

// ErrNoRecord is returned when a key has no record.
var ErrNoRecord = errors.New("store: no such record")

// RecordInput is a record to append.
type RecordInput struct {
	Kind, Key string
	Body      []byte
}

func (r RecordInput) check() error {
	if !recordKindRE.MatchString(r.Kind) {
		return fmt.Errorf("store: record kind %q must be lower-case letters, digits, dot or underscore", r.Kind)
	}
	if r.Key == "" || len(r.Key) > 300 {
		return errors.New("store: a record needs a key of 1-300 characters")
	}
	if len(r.Body) == 0 {
		return errors.New("store: a record needs a body")
	}
	return nil
}

// RecordHash is the content hash of a body.
func RecordHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Append writes records in one transaction. A record whose kind, key and
// content are already stored is left alone; the second result counts the new
// ones.
func (r Records) Append(ctx context.Context, in ...RecordInput) ([]Record, int, error) {
	h, err := r.t.handle()
	if err != nil {
		return nil, 0, err
	}
	for _, x := range in {
		if err := x.check(); err != nil {
			return nil, 0, err
		}
	}
	now := r.t.db.now()
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("store: records: %w", err)
	}
	defer tx.Rollback()
	out := make([]Record, 0, len(in))
	fresh := 0
	for _, x := range in {
		hash := RecordHash(x.Body)
		res, err := tx.ExecContext(ctx, `INSERT INTO records (kind, key, hash, body, created_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, x.Kind, x.Key, hash, string(x.Body), ms(now))
		if err != nil {
			return nil, 0, fmt.Errorf("store: records: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			fresh++
		}
		var rec Record
		var body string
		var created int64
		if err := tx.QueryRowContext(ctx, `SELECT seq, kind, key, hash, body, created_ms FROM records WHERE kind = ? AND key = ? AND hash = ?`, x.Kind, x.Key, hash).
			Scan(&rec.Seq, &rec.Kind, &rec.Key, &rec.Hash, &body, &created); err != nil {
			return nil, 0, fmt.Errorf("store: records: %w", err)
		}
		rec.Body, rec.At = []byte(body), fromMS(created)
		out = append(out, rec)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("store: records: %w", err)
	}
	return out, fresh, nil
}

func scanRecords(rows *sql.Rows) ([]Record, error) {
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var rec Record
		var body string
		var created int64
		if err := rows.Scan(&rec.Seq, &rec.Kind, &rec.Key, &rec.Hash, &body, &created); err != nil {
			return nil, err
		}
		rec.Body, rec.At = []byte(body), fromMS(created)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Latest is the newest record of a key, or ErrNoRecord.
func (r Records) Latest(ctx context.Context, kind, key string) (Record, error) {
	h, err := r.t.handle()
	if err != nil {
		return Record{}, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT seq, kind, key, hash, body, created_ms FROM records WHERE kind = ? AND key = ? ORDER BY seq DESC LIMIT 1`, kind, key)
	if err != nil {
		return Record{}, fmt.Errorf("store: records: %w", err)
	}
	recs, err := scanRecords(rows)
	if err != nil {
		return Record{}, fmt.Errorf("store: records: %w", err)
	}
	if len(recs) == 0 {
		return Record{}, ErrNoRecord
	}
	return recs[0], nil
}

// History is every revision of a key, oldest first.
func (r Records) History(ctx context.Context, kind, key string) ([]Record, error) {
	h, err := r.t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT seq, kind, key, hash, body, created_ms FROM records WHERE kind = ? AND key = ? ORDER BY seq`, kind, key)
	if err != nil {
		return nil, fmt.Errorf("store: records: %w", err)
	}
	return scanRecords(rows)
}

// Current lists the newest record of every key of a kind, ordered by key. A
// prefix limits it to keys that start with it.
func (r Records) Current(ctx context.Context, kind, prefix string) ([]Record, error) {
	h, err := r.t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT seq, kind, key, hash, body, created_ms FROM records
		WHERE kind = ? AND substr(key, 1, ?) = ? AND seq = (SELECT MAX(seq) FROM records x WHERE x.kind = records.kind AND x.key = records.key)
		ORDER BY key`, kind, len(prefix), prefix)
	if err != nil {
		return nil, fmt.Errorf("store: records: %w", err)
	}
	return scanRecords(rows)
}

// All lists every record of a kind in the order written, optionally only those
// whose key starts with the prefix. It is how an event log is read.
func (r Records) All(ctx context.Context, kind, prefix string) ([]Record, error) {
	h, err := r.t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT seq, kind, key, hash, body, created_ms FROM records WHERE kind = ? AND substr(key, 1, ?) = ? ORDER BY seq`, kind, len(prefix), prefix)
	if err != nil {
		return nil, fmt.Errorf("store: records: %w", err)
	}
	return scanRecords(rows)
}
