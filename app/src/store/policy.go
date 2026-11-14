package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/policy"
)

// Policies returns the policy store of one tenant. An invalid tenant ID fails on
// every call and creates nothing.
func (db *DB) Policies(tenantID string) policy.Store {
	return policyStore{tenantStore{db: db, id: tenantID, valid: model.ValidID(tenantID)}}
}

type policyStore struct{ t tenantStore }

var _ policy.Store = policyStore{}

// Append records an activation in one transaction: the version row if its
// content is new, and the activation row if the epoch is still the newest.
func (s policyStore) Append(ctx context.Context, a policy.Activation) (policy.Record, error) {
	h, err := s.t.handle()
	if err != nil {
		return policy.Record{}, err
	}
	content, err := policy.Canonical(a.Policy)
	if err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: %w", err)
	}
	if a.At.IsZero() {
		a.At = s.t.db.now()
	}
	changes, _ := json.Marshal(nonNil(a.Changes))
	effects, _ := json.Marshal(nonNilS(a.Effects))
	notes, _ := json.Marshal(nonNilS(a.RiskNotes))

	tx, err := h.beginWrite(ctx)
	if err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: %w", err)
	}
	defer tx.Rollback()

	var newest, newestAt int64
	err = tx.QueryRowContext(ctx, `SELECT seq, at_ms FROM policy_activation ORDER BY seq DESC LIMIT 1`).Scan(&newest, &newestAt)
	if errors.Is(err, sql.ErrNoRows) {
		newest, newestAt = 0, 0
	} else if err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: %w", err)
	}
	if newest != a.PrevEpoch {
		return policy.Record{}, policy.ErrStale
	}
	if ms(a.At) < newestAt {
		return policy.Record{}, fmt.Errorf("store: policy: activation at %s is before the previous one", a.At.Format(time.RFC3339))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO policy_version (hash, content, created_ms) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, a.Hash, string(content), ms(a.At)); err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: %w", err)
	}
	var vseq int64
	if err := tx.QueryRowContext(ctx, `SELECT seq FROM policy_version WHERE hash = ?`, a.Hash).Scan(&vseq); err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: %w", err)
	}
	risk := 0
	if a.RiskAccepted {
		risk = 1
	}
	var epoch int64
	err = tx.QueryRowContext(ctx, `INSERT INTO policy_activation (version_seq, prev_seq, at_ms, risk_accepted, risk_notes, changes, effects) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING seq`,
		vseq, a.PrevEpoch, ms(a.At), risk, string(notes), string(changes), string(effects)).Scan(&epoch)
	if err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: %w", err)
	}
	return policy.Record{Epoch: epoch, Hash: a.Hash, At: a.At.UTC().Truncate(time.Millisecond), Policy: a.Policy,
		RiskAccepted: a.RiskAccepted, RiskNotes: a.RiskNotes, Changes: a.Changes, Effects: a.Effects}, nil
}

const policySelect = `SELECT a.seq, v.hash, v.content, a.at_ms, a.risk_accepted, a.risk_notes, a.changes, a.effects
	FROM policy_activation a JOIN policy_version v ON v.seq = a.version_seq`

// AsOf returns the policy in force at t: the newest activation made at or
// before t. A zero t means now.
func (s policyStore) AsOf(ctx context.Context, t time.Time) (policy.Record, bool, error) {
	h, err := s.t.handle()
	if err != nil {
		return policy.Record{}, false, err
	}
	bound := int64(1) << 62
	if !t.IsZero() {
		bound = ms(t)
	}
	row := h.r.QueryRowContext(ctx, policySelect+` WHERE a.at_ms <= ? ORDER BY a.seq DESC LIMIT 1`, bound)
	rec, err := scanPolicy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return policy.Record{}, false, nil
	}
	if err != nil {
		return policy.Record{}, false, err
	}
	return rec, true, nil
}

// History returns every activation, oldest first.
func (s policyStore) History(ctx context.Context) ([]policy.Record, error) {
	h, err := s.t.handle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, policySelect+` ORDER BY a.seq`)
	if err != nil {
		return nil, fmt.Errorf("store: policy: %w", err)
	}
	defer rows.Close()
	var out []policy.Record
	for rows.Next() {
		rec, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanPolicy(r scanner) (policy.Record, error) {
	var rec policy.Record
	var content, notes, changes, effects string
	var at int64
	var risk int
	if err := r.Scan(&rec.Epoch, &rec.Hash, &content, &at, &risk, &notes, &changes, &effects); err != nil {
		return policy.Record{}, err
	}
	if err := json.Unmarshal([]byte(content), &rec.Policy); err != nil {
		return policy.Record{}, fmt.Errorf("store: policy: epoch %d: %w", rec.Epoch, err)
	}
	for _, f := range []struct {
		src string
		dst any
	}{{notes, &rec.RiskNotes}, {changes, &rec.Changes}, {effects, &rec.Effects}} {
		if err := json.Unmarshal([]byte(f.src), f.dst); err != nil {
			return policy.Record{}, fmt.Errorf("store: policy: epoch %d: %w", rec.Epoch, err)
		}
	}
	rec.At = fromMS(at)
	rec.RiskAccepted = risk == 1
	return rec, nil
}

func nonNil(c []policy.Change) []policy.Change {
	if c == nil {
		return []policy.Change{}
	}
	return c
}

func nonNilS(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
