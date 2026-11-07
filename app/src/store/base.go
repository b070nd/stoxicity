package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/scheduler"
)

const (
	// articleSelect lists the columns scanArticle reads. The article's score
	// is the latest one: newest created_ms, ties broken by the larger lens
	// version (compared as text).
	articleSelect = `SELECT a.id, a.source_id, a.domain, a.url, a.title, a.summary,
		a.published_ms, a.fetched_ms, a.form, a.items, a.tickers, a.topics, a.cluster_id, a.cik,
		s.lens_version, s.relevance, s.materiality, s.tone, s.kind, s.sector, s.reasons`

	latestScoreJoin = ` LEFT JOIN scores s ON s.article_id = a.id AND s.lens_version = (
		SELECT s2.lens_version FROM scores s2 WHERE s2.article_id = a.id
		ORDER BY s2.created_ms DESC, s2.lens_version DESC LIMIT 1)`

	articleOrder = ` ORDER BY a.published_ms DESC, a.id ASC`

	defaultSearchLimit = 100
	hasChunk           = 500
)

// Save stores articles in one transaction. A new article is inserted. For an
// existing one only title, summary, form, CIK, items, tickers, topics and cluster
// are updated; id, source, domain, URL, published and first-fetched times
// never change. A zero Fetched is stored as the time of the Save.
//
// An article's Score is stored per lens version with INSERT OR IGNORE: the
// score of a given version is never overwritten and a new version adds a row.
// A completely zero Score means "not scored" and stores nothing; a Score with
// any content but an empty LensVersion is an error and nothing is saved.
func (db *DB) Save(ctx context.Context, articles []model.Article) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	for _, a := range articles {
		if a.ID == "" {
			return errors.New("store: save: article without ID")
		}
		if !scoreIsZero(a.Score) && a.Score.LensVersion == "" {
			return fmt.Errorf("store: save: score of article %q has no lens version", a.ID)
		}
	}
	if len(articles) == 0 {
		return nil
	}
	now := db.now()

	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: save: %w", err)
	}
	defer tx.Rollback()
	var lastSeq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM articles`).Scan(&lastSeq); err != nil {
		return fmt.Errorf("store: save: %w", err)
	}
	upsert, err := tx.PrepareContext(ctx, `INSERT INTO articles
		(id, source_id, domain, url, title, summary, published_ms, fetched_ms, form, items, tickers, topics, cluster_id, cik)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			title = excluded.title, summary = excluded.summary, form = excluded.form,
			items = excluded.items, tickers = excluded.tickers, topics = excluded.topics,
			cluster_id = excluded.cluster_id, cik = excluded.cik
		WHERE title IS NOT excluded.title OR summary IS NOT excluded.summary OR form IS NOT excluded.form
			OR items IS NOT excluded.items OR tickers IS NOT excluded.tickers
			OR topics IS NOT excluded.topics OR cluster_id IS NOT excluded.cluster_id
			OR cik IS NOT excluded.cik`)
	if err != nil {
		return fmt.Errorf("store: save: %w", err)
	}
	defer upsert.Close()
	score, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO scores
		(article_id, lens_version, relevance, materiality, tone, kind, sector, reasons, created_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: save: %w", err)
	}
	defer score.Close()

	for _, a := range mergeDuplicates(articles) {
		fetched := a.Fetched
		if fetched.IsZero() {
			fetched = now
		}
		if _, err := upsert.ExecContext(ctx, a.ID, a.SourceID, a.Domain, a.URL, a.Title, a.Summary,
			ms(a.Published), ms(fetched), a.Form,
			encodeList(a.Items), encodeList(a.Tickers), encodeList(a.Topics), a.ClusterID, a.CIK); err != nil {
			return fmt.Errorf("store: save %q: %w", a.ID, err)
		}
		for _, s := range a.scores {
			if _, err := score.ExecContext(ctx, a.ID, s.LensVersion, s.Relevance, s.Materiality, s.Tone,
				string(s.Kind), s.Sector, encodeList(s.Reasons), ms(now)); err != nil {
				return fmt.Errorf("store: save score of %q: %w", a.ID, err)
			}
		}
	}
	// Rows added by this batch get sequence numbers above lastSeq; index them
	// in one statement. Existing rows were re-indexed by the update trigger.
	if _, err := tx.ExecContext(ctx, `INSERT INTO articles_fts (rowid, title, summary, tickers)
		SELECT seq, title, summary, tickers FROM articles WHERE seq > ?`, lastSeq); err != nil {
		return fmt.Errorf("store: save: index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: save: %w", err)
	}
	return nil
}

// scored is an article together with every non-empty score it came with.
type scored struct {
	model.Article
	scores []model.Score
}

// mergeDuplicates folds repeated IDs in one batch into a single entry, in
// first-seen order: the later copy supplies the fields that may change, the
// first supplies the rest, and every copy's score is kept (the first score of a
// lens version wins, as in the database).
func mergeDuplicates(articles []model.Article) []scored {
	out := make([]scored, 0, len(articles))
	at := make(map[string]int, len(articles))
	for _, a := range articles {
		i, seen := at[a.ID]
		if !seen {
			at[a.ID] = len(out)
			out = append(out, scored{Article: a})
			i = len(out) - 1
		} else {
			first := out[i].Article
			a.SourceID, a.Domain, a.URL, a.Published, a.Fetched = first.SourceID, first.Domain, first.URL, first.Published, first.Fetched
			out[i].Article = a
		}
		if !scoreIsZero(a.Score) {
			out[i].scores = append(out[i].scores, a.Score)
		}
	}
	return out
}

func scoreIsZero(s model.Score) bool {
	return s.Relevance == 0 && s.Materiality == 0 && s.Tone == 0 && s.Kind == "" &&
		s.Sector == "" && len(s.Reasons) == 0 && s.LensVersion == ""
}

// Day returns the articles published on the calendar day of day, taken in
// day's own location, ordered by published time (newest first), then ID.
func (db *DB) Day(ctx context.Context, day time.Time) ([]model.Article, error) {
	y, m, d := day.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, day.Location())
	return db.Range(ctx, start, start.AddDate(0, 0, 1))
}

// Range returns the articles published in [from, to), newest first, then by ID.
func (db *DB) Range(ctx context.Context, from, to time.Time) ([]model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	q := articleSelect + ` FROM articles a` + latestScoreJoin +
		` WHERE a.published_ms >= ? AND a.published_ms < ?` + articleOrder
	out, err := queryArticles(ctx, h.r, q, ms(from), ms(to))
	if err != nil {
		return nil, fmt.Errorf("store: range: %w", err)
	}
	return out, nil
}

// Get returns one article with its latest score. A missing id gives an error
// wrapping ErrNotFound.
func (db *DB) Get(ctx context.Context, id string) (model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return model.Article{}, err
	}
	q := articleSelect + ` FROM articles a` + latestScoreJoin + ` WHERE a.id = ?`
	out, err := queryArticles(ctx, h.r, q, id)
	if err != nil {
		return model.Article{}, fmt.Errorf("store: get: %w", err)
	}
	if len(out) == 0 {
		return model.Article{}, fmt.Errorf("%w: article %q", ErrNotFound, id)
	}
	return out[0], nil
}

// Search returns the articles whose title, summary or tickers match an FTS5
// MATCH expression (words, "phrases", OR, NOT, prefix*, and column filters
// such as tickers:AAPL). Only articles published in [from, to) are returned; a
// zero from or to leaves that side open. Results are ordered newest first;
// limit <= 0 means 100. An empty or malformed expression is an error.
func (db *DB) Search(ctx context.Context, match string, from, to time.Time, limit int) ([]model.Article, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(match) == "" {
		return nil, errors.New("store: search: empty match expression")
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	q := articleSelect + ` FROM articles_fts JOIN articles a ON a.seq = articles_fts.rowid` +
		latestScoreJoin + ` WHERE articles_fts MATCH ?`
	args := []any{match}
	if !from.IsZero() {
		q += ` AND a.published_ms >= ?`
		args = append(args, ms(from))
	}
	if !to.IsZero() {
		q += ` AND a.published_ms < ?`
		args = append(args, ms(to))
	}
	q += articleOrder + ` LIMIT ?`
	args = append(args, limit)
	out, err := queryArticles(ctx, h.r, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: search: %w", err)
	}
	return out, nil
}

// Has reports which of ids are stored. Every stored id is a key of the result
// with value true; ids that are not stored are absent. It is meant for
// deduplication before fetching or scoring.
func (db *DB) Has(ctx context.Context, ids []string) (map[string]bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	found := make(map[string]bool, len(ids))
	for start := 0; start < len(ids); start += hasChunk {
		chunk := ids[start:min(start+hasChunk, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		q := `SELECT id FROM articles WHERE id IN (?` + strings.Repeat(`,?`, len(chunk)-1) + `)`
		rows, err := h.r.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("store: has: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: has: %w", err)
			}
			found[id] = true
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return nil, fmt.Errorf("store: has: %w", err)
		}
	}
	return found, nil
}

// FilterVisible returns the articles whose SourceID is true in visible,
// keeping their order. A tenant's visible sources come from its configuration.
func FilterVisible(articles []model.Article, visible map[string]bool) []model.Article {
	out := make([]model.Article, 0, len(articles))
	for _, a := range articles {
		if visible[a.SourceID] {
			out = append(out, a)
		}
	}
	return out
}

// LoadSource returns the saved fetch State and Health of a source. An unknown
// source gives zero values and a nil error.
func (db *DB) LoadSource(ctx context.Context, sourceID string) (feeds.State, scheduler.Health, error) {
	var st feeds.State
	var hl scheduler.Health
	h, err := db.baseHandle()
	if err != nil {
		return st, hl, err
	}
	var stateJSON, healthJSON string
	err = h.r.QueryRowContext(ctx, `SELECT state, health FROM source_state WHERE source_id = ?`, sourceID).
		Scan(&stateJSON, &healthJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return st, hl, nil
	}
	if err != nil {
		return st, hl, fmt.Errorf("store: load source %q: %w", sourceID, err)
	}
	if err := json.Unmarshal([]byte(stateJSON), &st); err != nil {
		return feeds.State{}, hl, fmt.Errorf("store: load source %q: %w", sourceID, err)
	}
	if err := json.Unmarshal([]byte(healthJSON), &hl); err != nil {
		return feeds.State{}, scheduler.Health{}, fmt.Errorf("store: load source %q: %w", sourceID, err)
	}
	return st, hl, nil
}

// SaveSource stores the fetch State and Health of a source, replacing what was
// saved before.
func (db *DB) SaveSource(ctx context.Context, sourceID string, st feeds.State, hl scheduler.Health) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	if sourceID == "" {
		return errors.New("store: save source: empty source id")
	}
	stateJSON, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("store: save source %q: %w", sourceID, err)
	}
	healthJSON, err := json.Marshal(hl)
	if err != nil {
		return fmt.Errorf("store: save source %q: %w", sourceID, err)
	}
	_, err = h.w.ExecContext(ctx, `INSERT INTO source_state (source_id, state, health, updated_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (source_id) DO UPDATE SET
			state = excluded.state, health = excluded.health, updated_ms = excluded.updated_ms`,
		sourceID, string(stateJSON), string(healthJSON), ms(db.now()))
	if err != nil {
		return fmt.Errorf("store: save source %q: %w", sourceID, err)
	}
	return nil
}

// queryArticles runs a query built from articleSelect and scans the rows.
func queryArticles(ctx context.Context, q querier, query string, args ...any) ([]model.Article, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func scanArticle(rows *sql.Rows) (model.Article, error) {
	var (
		a                           model.Article
		pubMS, fetMS                int64
		items, tickers, topics      string
		lens, kind, sector, reasons sql.NullString
		relevance, tone             sql.NullFloat64
		materiality                 sql.NullInt64
	)
	err := rows.Scan(&a.ID, &a.SourceID, &a.Domain, &a.URL, &a.Title, &a.Summary,
		&pubMS, &fetMS, &a.Form, &items, &tickers, &topics, &a.ClusterID, &a.CIK,
		&lens, &relevance, &materiality, &tone, &kind, &sector, &reasons)
	if err != nil {
		return a, err
	}
	a.Published, a.Fetched = fromMS(pubMS), fromMS(fetMS)
	if a.Items, err = decodeList(items); err != nil {
		return a, fmt.Errorf("article %q: %w", a.ID, err)
	}
	if a.Tickers, err = decodeList(tickers); err != nil {
		return a, fmt.Errorf("article %q: %w", a.ID, err)
	}
	if a.Topics, err = decodeList(topics); err != nil {
		return a, fmt.Errorf("article %q: %w", a.ID, err)
	}
	if lens.Valid {
		a.Score = model.Score{
			Relevance:   relevance.Float64,
			Materiality: int(materiality.Int64),
			Tone:        tone.Float64,
			Kind:        model.EventKind(kind.String),
			Sector:      sector.String,
			LensVersion: lens.String,
		}
		if a.Score.Reasons, err = decodeList(reasons.String); err != nil {
			return a, fmt.Errorf("article %q: %w", a.ID, err)
		}
	}
	return a, nil
}

// encodeList stores a string list as a JSON array; nil and empty are "[]".
func encodeList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(v) // a []string always marshals
	return string(b)
}

// decodeList is the inverse of encodeList; an empty list comes back as nil.
func decodeList(s string) ([]string, error) {
	var v []string
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return nil, nil
	}
	return v, nil
}
