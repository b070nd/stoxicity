package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/origin"
)

// PutOrigins appends the origins found for articles. An origin that equals the
// article's latest stored one (same rule version, kind, key, derivation, copy
// and method) adds nothing, so detecting again is harmless; a different finding
// is a new row, and the old one stays for readers as of an earlier time.
func (db *DB) PutOrigins(ctx context.Context, os []origin.Origin, at time.Time) (int, error) {
	if len(os) == 0 {
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
		return 0, fmt.Errorf("store: origins: %w", err)
	}
	defer tx.Rollback()
	wrote := 0
	for _, o := range os {
		if o.ArticleID == "" || o.Key == "" || o.Kind == "" {
			return 0, errors.New("store: origins: origin without article, key or kind")
		}
		var cur origin.Origin
		err := tx.QueryRowContext(ctx, `SELECT version, kind, origin_key, derived_from, copy_of, suspected_from, method FROM article_origin WHERE article_id = ? ORDER BY seq DESC LIMIT 1`, o.ArticleID).
			Scan(&cur.Version, &cur.Kind, &cur.Key, &cur.DerivedFrom, &cur.CopyOf, &cur.SuspectedFrom, &cur.Method)
		if err == nil && cur.Version == o.Version && cur.Kind == o.Kind && cur.Key == o.Key && cur.DerivedFrom == o.DerivedFrom && cur.CopyOf == o.CopyOf && cur.SuspectedFrom == o.SuspectedFrom && cur.Method == o.Method {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("store: origins: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO article_origin (article_id, version, publisher, kind, origin_key, derived_from, copy_of, suspected_from, method, confidence, detail, detected_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, o.ArticleID, o.Version, o.Publisher, string(o.Kind), o.Key, o.DerivedFrom, o.CopyOf, o.SuspectedFrom, string(o.Method), o.Confidence, o.Detail, ms(at)); err != nil {
			return 0, fmt.Errorf("store: origins: %w", err)
		}
		for _, m := range o.Matches {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO origin_match (article_id, other_id, version, method, score, shared_runs, longest_run, passage, detected_ms)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, o.ArticleID, m.Other, o.Version, string(m.Method), m.Score, m.Shared, m.LongestRun, m.Passage, ms(at)); err != nil {
				return 0, fmt.Errorf("store: origins: %w", err)
			}
		}
		wrote++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: origins: %w", err)
	}
	return wrote, nil
}

// Origins returns, for each article that has one, the origin as it was
// understood at asOf (zero: now): the latest finding made at or before it.
func (db *DB) Origins(ctx context.Context, articleIDs []string, asOf time.Time) (map[string]origin.Origin, error) {
	out := map[string]origin.Origin{}
	if len(articleIDs) == 0 {
		return out, nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !asOf.IsZero() {
		bound = ms(asOf)
	}
	for start := 0; start < len(articleIDs); start += 400 {
		chunk := articleIDs[start:min(start+400, len(articleIDs))]
		args := make([]any, 0, len(chunk)+1)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := h.r.QueryContext(ctx, `SELECT o.article_id, o.version, o.publisher, o.kind, o.origin_key, o.derived_from, o.copy_of, o.suspected_from, o.method, o.confidence, o.detail
			FROM article_origin o WHERE o.article_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)
			AND o.seq = (SELECT MAX(o2.seq) FROM article_origin o2 WHERE o2.article_id = o.article_id AND o2.detected_ms <= ?)`,
			append(args, bound)...)
		if err != nil {
			return nil, fmt.Errorf("store: origins: %w", err)
		}
		for rows.Next() {
			var o origin.Origin
			var kind, method string
			if err := rows.Scan(&o.ArticleID, &o.Version, &o.Publisher, &kind, &o.Key, &o.DerivedFrom, &o.CopyOf, &o.SuspectedFrom, &method, &o.Confidence, &o.Detail); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: origins: %w", err)
			}
			o.Kind, o.Method = origin.Kind(kind), origin.Method(method)
			out[o.ArticleID] = o
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: origins: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// UnassessedArticles returns the IDs of the articles that are evidence of an
// event and have no origin yet, oldest event first, at most limit.
func (db *DB) UnassessedArticles(ctx context.Context, limit int) ([]string, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT DISTINCT v.article_id FROM event_evidence v
		WHERE NOT EXISTS (SELECT 1 FROM article_origin o WHERE o.article_id = v.article_id) LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: origins: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: origins: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EpisodeDecision is a stated decision on whether two events of one security
// are one episode.
type EpisodeDecision struct {
	SecurityID string
	EventA     string // EventA < EventB
	EventB     string
	Decision   string // same | distinct | undecided
	Basis      string
	Method     string
	Confidence float64
	Version    string
	DecidedAt  time.Time
}

// Episode decisions.
const (
	DecisionSame      = "same"
	DecisionDistinct  = "distinct"
	DecisionUndecided = "undecided"
)

// PutEpisodeDecisions appends decisions. A decision equal to the latest one for
// the pair (decision, basis, method, version) adds nothing.
func (db *DB) PutEpisodeDecisions(ctx context.Context, ds []EpisodeDecision, at time.Time) (int, error) {
	if len(ds) == 0 {
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
		return 0, fmt.Errorf("store: episodes: %w", err)
	}
	defer tx.Rollback()
	wrote := 0
	for _, d := range ds {
		if d.EventA == "" || d.EventB == "" || d.SecurityID == "" || d.EventA >= d.EventB {
			return 0, errors.New("store: episodes: a decision needs a security and two events in order")
		}
		var cur EpisodeDecision
		err := tx.QueryRowContext(ctx, `SELECT decision, basis, method, version FROM episode_decision WHERE security_id = ? AND event_a = ? AND event_b = ? ORDER BY seq DESC LIMIT 1`,
			d.SecurityID, d.EventA, d.EventB).Scan(&cur.Decision, &cur.Basis, &cur.Method, &cur.Version)
		if err == nil && cur.Decision == d.Decision && cur.Basis == d.Basis && cur.Method == d.Method && cur.Version == d.Version {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("store: episodes: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO episode_decision (security_id, event_a, event_b, decision, basis, method, confidence, version, decided_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.SecurityID, d.EventA, d.EventB, d.Decision, d.Basis, d.Method, d.Confidence, d.Version, ms(at)); err != nil {
			return 0, fmt.Errorf("store: episodes: %w", err)
		}
		wrote++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: episodes: %w", err)
	}
	return wrote, nil
}

// EpisodeDecisions returns, for each pair of events of the security that has
// one, the latest decision made at or before asOf (zero: now).
func (db *DB) EpisodeDecisions(ctx context.Context, securityID string, asOf time.Time) (map[[2]string]EpisodeDecision, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !asOf.IsZero() {
		bound = ms(asOf)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT d.event_a, d.event_b, d.decision, d.basis, d.method, d.confidence, d.version, d.decided_ms FROM episode_decision d
		WHERE d.security_id = ? AND d.decided_ms <= ? AND d.seq = (SELECT MAX(d2.seq) FROM episode_decision d2
			WHERE d2.security_id = d.security_id AND d2.event_a = d.event_a AND d2.event_b = d.event_b AND d2.decided_ms <= ?)`, securityID, bound, bound)
	if err != nil {
		return nil, fmt.Errorf("store: episodes: %w", err)
	}
	defer rows.Close()
	out := map[[2]string]EpisodeDecision{}
	for rows.Next() {
		d := EpisodeDecision{SecurityID: securityID}
		var at int64
		if err := rows.Scan(&d.EventA, &d.EventB, &d.Decision, &d.Basis, &d.Method, &d.Confidence, &d.Version, &at); err != nil {
			return nil, fmt.Errorf("store: episodes: %w", err)
		}
		d.DecidedAt = fromMS(at)
		out[[2]string{d.EventA, d.EventB}] = d
	}
	return out, rows.Err()
}

// OriginMatches returns the comparisons recorded for an article at or before
// asOf (zero: now), oldest first: what was seen when it was found to be a copy
// of, or suspected to derive from, another article.
func (db *DB) OriginMatches(ctx context.Context, articleID string, asOf time.Time) ([]origin.Match, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !asOf.IsZero() {
		bound = ms(asOf)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT other_id, method, score, shared_runs, longest_run, passage FROM origin_match
		WHERE article_id = ? AND detected_ms <= ? ORDER BY seq`, articleID, bound)
	if err != nil {
		return nil, fmt.Errorf("store: origin matches: %w", err)
	}
	defer rows.Close()
	var out []origin.Match
	for rows.Next() {
		var m origin.Match
		var method string
		if err := rows.Scan(&m.Other, &method, &m.Score, &m.Shared, &m.LongestRun, &m.Passage); err != nil {
			return nil, fmt.Errorf("store: origin matches: %w", err)
		}
		m.Method = origin.Method(method)
		out = append(out, m)
	}
	return out, rows.Err()
}
