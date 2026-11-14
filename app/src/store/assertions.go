package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/model"
)

type partyJSON struct {
	Ticker string `json:"ticker"`
	Role   string `json:"role"`
	Basis  string `json:"basis"`
}

// PutAssertions stores the claims of articles. A claim already stored (same
// article, version, extractor and number) is left as it was; the number of rows
// written is returned.
func (db *DB) PutAssertions(ctx context.Context, results []assertion.Result, at time.Time) (int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: assertions: %w", err)
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO assertion
		(article_id, version, extractor, seq, kind, method, item, span, span_start, span_end, match, parties, negated, denied, historical, anticipatory,
		 status, attribution, magnitude, routine, rated, model, prompt_version, truncated, recorded_ms, action)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("store: assertions: %w", err)
	}
	defer st.Close()
	n := 0
	for _, r := range results {
		for _, a := range r.Assertions {
			ps := make([]partyJSON, len(a.Parties))
			for i, p := range a.Parties {
				ps[i] = partyJSON{p.Ticker, string(p.Role), p.Basis}
			}
			pj, _ := json.Marshal(ps)
			res, err := st.ExecContext(ctx, r.ArticleID, r.Version, r.Extractor, a.Seq, string(a.Kind), a.Method, a.Item, a.Span, a.Start, a.End, a.Match, string(pj),
				b2i(a.Negated), b2i(a.Denied), b2i(a.Historical), b2i(a.Anticipatory), string(a.Status), string(a.Attribution), a.Magnitude, a.Routine, a.Rated,
				r.Model, r.PromptVersion, b2i(r.Truncated), ms(at), a.Action)
			if err != nil {
				return 0, fmt.Errorf("store: assertions: %w", err)
			}
			if k, _ := res.RowsAffected(); k > 0 {
				n++
			}
		}
	}
	return n, tx.Commit()
}

// Assertions returns the claims of the articles that had been recorded by the
// time (zero: all), by article.
func (db *DB) Assertions(ctx context.Context, articleIDs []string, asOf time.Time) (map[string]assertion.Result, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	out := map[string]assertion.Result{}
	if len(articleIDs) == 0 {
		return out, nil
	}
	limit := int64(1) << 62
	if !asOf.IsZero() {
		limit = ms(asOf)
	}
	type readKey struct{ id, version, extractor string }
	type readEntry struct {
		r   assertion.Result
		rec int64
	}
	read := map[readKey]*readEntry{}
	for start := 0; start < len(articleIDs); start += 400 {
		chunk := articleIDs[start:min(start+400, len(articleIDs))]
		args := []any{limit}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := h.r.QueryContext(ctx, `SELECT article_id, version, extractor, seq, kind, method, item, span, span_start, span_end, match, parties, negated, denied,
			historical, anticipatory, status, attribution, magnitude, routine, rated, model, prompt_version, truncated, action, recorded_ms
			FROM assertion WHERE recorded_ms <= ? AND article_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`) ORDER BY article_id, version, extractor, seq`, args...)
		if err != nil {
			return nil, fmt.Errorf("store: assertions: %w", err)
		}
		for rows.Next() {
			var id, version, extractor, pj string
			var a assertion.Assertion
			var kind, status, attr string
			var neg, den, his, ant, trunc int
			var mdl, pv string
			var rec int64
			if err := rows.Scan(&id, &version, &extractor, &a.Seq, &kind, &a.Method, &a.Item, &a.Span, &a.Start, &a.End, &a.Match, &pj, &neg, &den, &his, &ant,
				&status, &attr, &a.Magnitude, &a.Routine, &a.Rated, &mdl, &pv, &trunc, &a.Action, &rec); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: assertions: %w", err)
			}
			a.Kind, a.Status, a.Attribution = model.EventKind(kind), assertion.Status(status), assertion.Attribution(attr)
			a.Negated, a.Denied, a.Historical, a.Anticipatory = neg == 1, den == 1, his == 1, ant == 1
			var ps []partyJSON
			_ = json.Unmarshal([]byte(pj), &ps)
			for _, p := range ps {
				a.Parties = append(a.Parties, assertion.Party{Ticker: p.Ticker, Role: assertion.Role(p.Role), Basis: p.Basis})
			}
			k := readKey{id, version, extractor}
			e := read[k]
			if e == nil {
				e = &readEntry{rec: rec}
				e.r.ArticleID, e.r.Version, e.r.Extractor, e.r.Model, e.r.PromptVersion, e.r.Truncated = id, version, extractor, mdl, pv, trunc == 1
				read[k] = e
			}
			e.rec = max(e.rec, rec)
			e.r.Assertions = append(e.r.Assertions, a)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: assertions: %w", err)
		}
		rows.Close()
	}
	// An article read by several versions of the rules has the claims of the
	// version recorded last: versions are alternative readings of one text, never
	// parts of one reading.
	for k, e := range read {
		if cur, ok := out[k.id]; ok {
			if prev := read[readKey{k.id, cur.Version, cur.Extractor}]; prev != nil && (prev.rec > e.rec || (prev.rec == e.rec && cur.Version >= k.version)) {
				continue
			}
		}
		out[k.id] = e.r
	}
	return out, nil
}

// PutAssessments appends the readings that differ from the latest one of their
// article, company, dimension and version (in value, label or abstention); a
// reading that says the same again is not stored. It returns how many it wrote.
func (db *DB) PutAssessments(ctx context.Context, as []assertion.Assessment, at time.Time) (int, error) {
	h, err := db.baseHandle()
	if err != nil {
		return 0, err
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: assessments: %w", err)
	}
	defer tx.Rollback()
	n := 0
	for _, a := range as {
		var value, abst int
		var label string
		err := tx.QueryRowContext(ctx, `SELECT value, label, abstained FROM assessment WHERE article_id = ? AND ticker = ? AND dimension = ? AND version = ?
			ORDER BY assessed_ms DESC LIMIT 1`, a.ArticleID, a.Ticker, a.Dimension, a.Version).Scan(&value, &label, &abst)
		if err == nil && value == a.Value && label == a.Label && abst == b2i(a.Abstained) {
			continue
		}
		sj, _ := json.Marshal(a.Seqs)
		if _, err := tx.ExecContext(ctx, `INSERT INTO assessment (article_id, ticker, dimension, version, value, label, abstained, basis, seqs, assessed_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, a.ArticleID, a.Ticker, a.Dimension, a.Version, a.Value, a.Label, b2i(a.Abstained), a.Basis, string(sj), ms(at)); err != nil {
			return 0, fmt.Errorf("store: assessments: %w", err)
		}
		n++
	}
	return n, tx.Commit()
}

// Assessments returns the readings in force at the time (zero: now) for the
// article, one per company, dimension and version, the latest made by then.
func (db *DB) Assessments(ctx context.Context, articleID string, asOf time.Time) ([]assertion.Assessment, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	limit := int64(1) << 62
	if !asOf.IsZero() {
		limit = ms(asOf)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT a.article_id, a.ticker, a.dimension, a.version, a.value, a.label, a.abstained, a.basis, a.seqs
		FROM assessment a WHERE a.article_id = ? AND a.assessed_ms = (SELECT MAX(b.assessed_ms) FROM assessment b
			WHERE b.article_id = a.article_id AND b.ticker = a.ticker AND b.dimension = a.dimension AND b.version = a.version AND b.assessed_ms <= ?)
		ORDER BY a.ticker, a.version, a.dimension`, articleID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: assessments: %w", err)
	}
	defer rows.Close()
	var out []assertion.Assessment
	for rows.Next() {
		var a assertion.Assessment
		var abst int
		var sj string
		if err := rows.Scan(&a.ArticleID, &a.Ticker, &a.Dimension, &a.Version, &a.Value, &a.Label, &abst, &a.Basis, &sj); err != nil {
			return nil, fmt.Errorf("store: assessments: %w", err)
		}
		a.Abstained = abst == 1
		_ = json.Unmarshal([]byte(sj), &a.Seqs)
		out = append(out, a)
	}
	return out, rows.Err()
}
