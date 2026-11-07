package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Events is the append-only history of what happened to which security.
type Events interface {
	// PutEvents appends a revision to each event whose understanding changed (see
	// EventInput) and reports how many revisions were written.
	PutEvents(ctx context.Context, in []EventInput, at time.Time) (int, error)
	// SecurityTimeline lists the events that concern the security with the
	// ticker, newest first, as the latest revision known at knownAt (zero: now).
	SecurityTimeline(ctx context.Context, ticker string, knownAt time.Time, limit int) ([]TimelineEntry, error)
	// EventHistory returns every revision of an event, oldest first.
	EventHistory(ctx context.Context, eventID string) ([]model.EventRevision, error)
	// SyncSecurities brings the securities and their ticker labels in line with
	// the reference list of tickers.
	SyncSecurities(ctx context.Context, at time.Time) (SecuritySync, error)
}

var _ Events = (*DB)(nil)

// EventInput is what is understood of an event from the evidence of one
// ingest. The revision it makes joins this with the evidence and securities of
// the event's latest revision, so a later article of the same story adds to the
// event instead of replacing it.
type EventInput struct {
	EventID      string
	Kind         model.EventKind
	Title        string
	EffectiveDay string
	PublicAt     time.Time
	Materiality  int
	Relevance    float64
	LensVersion  string
	Securities   []model.EventSecurity // Ticker is resolved to a SecurityID
	Evidence     []model.Evidence
	Topics       []string
	Rationale    string
	Tone         float64
}

// SecuritySync says what SyncSecurities did.
type SecuritySync struct{ Created, Relabelled, Linked int }

// TimelineEntry is one event on a security's timeline.
type TimelineEntry struct {
	EventID     string
	Revision    int
	Kind        model.EventKind
	Status      string
	Title       string
	Role        model.Role
	PublicAt    time.Time
	RecordedAt  time.Time
	Materiality int
	Relevance   float64
	Evidence    int // observations the revision rests on
}

func securityID(label string, at time.Time) string {
	sum := sha256.Sum256([]byte(label + "|" + at.UTC().Format(time.RFC3339Nano)))
	return "sec_" + hex.EncodeToString(sum[:6])
}

// ensureSecurity returns the security that carries the ticker today, creating
// it when there is none. The ticker is a label with a first day.
func ensureSecurity(ctx context.Context, tx *sql.Tx, ticker string, at time.Time) (string, error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	var id string
	err := tx.QueryRowContext(ctx, `SELECT security_id FROM security_ticker WHERE ticker = ? AND to_day = '' ORDER BY from_day DESC LIMIT 1`, ticker).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var cik, name string
	switch err := tx.QueryRowContext(ctx, `SELECT cik, name FROM ref_ticker WHERE ticker = ?`, ticker).Scan(&cik, &name); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return "", err
	}
	id = securityID(ticker, at)
	if _, err := tx.ExecContext(ctx, `INSERT INTO security (id, cik, name, created_ms) VALUES (?, ?, ?, ?)`, id, cik, name, ms(at)); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO security_ticker (security_id, ticker, from_day) VALUES (?, ?, ?)`, id, ticker, at.UTC().Format(time.DateOnly)); err != nil {
		return "", err
	}
	return id, nil
}

// SyncSecurities implements Events.
func (db *DB) SyncSecurities(ctx context.Context, at time.Time) (SecuritySync, error) {
	var rep SecuritySync
	h, err := db.baseHandle()
	if err != nil {
		return rep, err
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return rep, fmt.Errorf("store: sync securities: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT ticker, name, cik FROM ref_ticker ORDER BY ticker`)
	if err != nil {
		return rep, fmt.Errorf("store: sync securities: %w", err)
	}
	type ref struct{ ticker, name, cik string }
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.ticker, &r.name, &r.cik); err != nil {
			rows.Close()
			return rep, fmt.Errorf("store: sync securities: %w", err)
		}
		refs = append(refs, r)
	}
	rows.Close()
	day := at.UTC().Format(time.DateOnly)
	for _, r := range refs {
		var id, cik string
		err := tx.QueryRowContext(ctx, `SELECT s.id, s.cik FROM security_ticker t JOIN security s ON s.id = t.security_id
			WHERE t.ticker = ? AND t.to_day = '' ORDER BY t.from_day DESC LIMIT 1`, r.ticker).Scan(&id, &cik)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id = securityID(r.ticker, at)
			if _, err := tx.ExecContext(ctx, `INSERT INTO security (id, cik, name, created_ms) VALUES (?, ?, ?, ?)`, id, r.cik, r.name, ms(at)); err != nil {
				return rep, fmt.Errorf("store: sync securities: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO security_ticker (security_id, ticker, from_day) VALUES (?, ?, ?)`, id, r.ticker, day); err != nil {
				return rep, fmt.Errorf("store: sync securities: %w", err)
			}
			rep.Created++
		case err != nil:
			return rep, fmt.Errorf("store: sync securities: %w", err)
		case cik == "":
			// Created from a mention before the reference list knew the company.
			if _, err := tx.ExecContext(ctx, `UPDATE security SET cik = ?, name = ? WHERE id = ?`, r.cik, r.name, id); err != nil {
				return rep, fmt.Errorf("store: sync securities: %w", err)
			}
			rep.Linked++
		case cik != r.cik:
			// The label now belongs to another company: it is reused. The old
			// security keeps its history; the label moves to a new one.
			if _, err := tx.ExecContext(ctx, `UPDATE security_ticker SET to_day = ? WHERE security_id = ? AND ticker = ? AND to_day = ''`, day, id, r.ticker); err != nil {
				return rep, fmt.Errorf("store: sync securities: %w", err)
			}
			nid := securityID(r.ticker+"|"+r.cik, at)
			if _, err := tx.ExecContext(ctx, `INSERT INTO security (id, cik, name, created_ms) VALUES (?, ?, ?, ?)`, nid, r.cik, r.name, ms(at)); err != nil {
				return rep, fmt.Errorf("store: sync securities: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO security_ticker (security_id, ticker, from_day) VALUES (?, ?, ?)`, nid, r.ticker, day); err != nil {
				return rep, fmt.Errorf("store: sync securities: %w", err)
			}
			rep.Relabelled++
		}
	}
	if err := tx.Commit(); err != nil {
		return rep, fmt.Errorf("store: sync securities: %w", err)
	}
	return rep, nil
}

// PutEvents implements Events.
func (db *DB) PutEvents(ctx context.Context, in []EventInput, at time.Time) (int, error) {
	if len(in) == 0 {
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
		return 0, fmt.Errorf("store: events: %w", err)
	}
	defer tx.Rollback()
	written := 0
	for _, e := range in {
		if e.EventID == "" {
			return 0, errors.New("store: events: event without ID")
		}
		wrote, err := putEvent(ctx, tx, e, at)
		if err != nil {
			return 0, fmt.Errorf("store: event %q: %w", e.EventID, err)
		}
		if wrote {
			written++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: events: %w", err)
	}
	return written, nil
}

func putEvent(ctx context.Context, tx *sql.Tx, e EventInput, at time.Time) (bool, error) {
	var last int
	var lastFP, lastStatus string
	var lastPublic int64
	err := tx.QueryRowContext(ctx, `SELECT revision, fingerprint, status, public_ms FROM event_revision WHERE event_id = ? ORDER BY revision DESC LIMIT 1`, e.EventID).
		Scan(&last, &lastFP, &lastStatus, &lastPublic)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	// Securities: this ingest's, with the previous revision's; the stronger role wins.
	roles := map[string]model.Role{}
	merge := func(id string, r model.Role) {
		if r.Strength() > roles[id].Strength() {
			roles[id] = r
		}
	}
	for _, s := range e.Securities {
		id := s.SecurityID
		if id == "" {
			if id, err = ensureSecurity(ctx, tx, s.Ticker, at); err != nil {
				return false, err
			}
		}
		merge(id, s.Role)
	}
	evidence := map[model.Evidence]bool{}
	for _, ev := range e.Evidence {
		evidence[ev] = true
	}
	topics := map[string]bool{}
	for _, t := range e.Topics {
		topics[t] = true
	}
	if last > 0 {
		rows, err := tx.QueryContext(ctx, `SELECT security_id, role FROM event_security WHERE event_id = ? AND revision = ?`, e.EventID, last)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var id, role string
			if err := rows.Scan(&id, &role); err != nil {
				rows.Close()
				return false, err
			}
			merge(id, model.Role(role))
		}
		rows.Close()
		rows, err = tx.QueryContext(ctx, `SELECT article_id, source_id FROM event_evidence WHERE event_id = ? AND revision = ?`, e.EventID, last)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var ev model.Evidence
			if err := rows.Scan(&ev.ArticleID, &ev.SourceID); err != nil {
				rows.Close()
				return false, err
			}
			evidence[ev] = true
		}
		rows.Close()
		rows, err = tx.QueryContext(ctx, `SELECT topic FROM event_topic WHERE event_id = ? AND revision = ?`, e.EventID, last)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				rows.Close()
				return false, err
			}
			topics[t] = true
		}
		rows.Close()
	}

	// A source that changed its own text about the article is a correction.
	status := model.StatusReported
	if lastStatus == model.StatusCorrected {
		status = model.StatusCorrected
	}
	for _, ev := range e.Evidence {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation WHERE article_id = ? AND source_id = ?`, ev.ArticleID, ev.SourceID).Scan(&n); err != nil {
			return false, err
		}
		if n > 1 {
			status = model.StatusCorrected
		}
	}

	public := ms(e.PublicAt)
	if last > 0 && lastPublic < public {
		public = lastPublic // the earliest evidence is when the world could know
	}
	secs := sortedKeys(roles)
	var evs []model.Evidence
	for ev := range evidence {
		evs = append(evs, ev)
	}
	slices.SortFunc(evs, func(a, b model.Evidence) int {
		return strings.Compare(a.ArticleID+"\x00"+a.SourceID, b.ArticleID+"\x00"+b.SourceID)
	})
	var tps []string
	for t := range topics {
		tps = append(tps, t)
	}
	slices.Sort(tps)

	var fp strings.Builder
	fmt.Fprintf(&fp, "%s|%s|%s|%s|%s\n", e.Kind, status, e.Title, e.EffectiveDay, strings.Join(tps, ","))
	for _, id := range secs {
		fmt.Fprintf(&fp, "s:%s:%s\n", id, roles[id])
	}
	for _, ev := range evs {
		fmt.Fprintf(&fp, "e:%s:%s\n", ev.ArticleID, ev.SourceID)
	}
	sum := sha256.Sum256([]byte(fp.String()))
	fingerprint := hex.EncodeToString(sum[:12])

	wrote := fingerprint != lastFP
	rev := last
	if wrote {
		if last == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO event (id, created_ms) VALUES (?, ?)`, e.EventID, ms(at)); err != nil {
				return false, err
			}
		}
		rev = last + 1
		if _, err := tx.ExecContext(ctx, `INSERT INTO event_revision (event_id, revision, kind, status, title, effective_day, public_ms, recorded_ms,
			materiality, relevance, lens_version, fingerprint, supersedes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.EventID, rev, string(e.Kind), status, e.Title, e.EffectiveDay, public, ms(at),
			e.Materiality, e.Relevance, e.LensVersion, fingerprint, last); err != nil {
			return false, err
		}
		for _, id := range secs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO event_security (event_id, revision, security_id, role) VALUES (?, ?, ?, ?)`, e.EventID, rev, id, string(roles[id])); err != nil {
				return false, err
			}
		}
		for _, ev := range evs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO event_evidence (event_id, revision, article_id, source_id) VALUES (?, ?, ?, ?)`, e.EventID, rev, ev.ArticleID, ev.SourceID); err != nil {
				return false, err
			}
		}
		for _, t := range tps {
			if _, err := tx.ExecContext(ctx, `INSERT INTO event_topic (event_id, revision, topic) VALUES (?, ?, ?)`, e.EventID, rev, t); err != nil {
				return false, err
			}
		}
	}
	// An assessment is an interpretation by one rule version; a new rule version
	// adds its own to the same revision, it does not make a new revision.
	if e.LensVersion != "" {
		for _, id := range secs {
			if roles[id] != model.RoleSubject {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO event_assessment (event_id, revision, security_id, rule, effect, direction, horizon,
				confidence, rationale, assessed_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				e.EventID, rev, id, e.LensVersion, fmt.Sprintf("materiality:%d", e.Materiality), model.DirectionUnknown, "unspecified",
				e.Relevance, e.Rationale, ms(at)); err != nil {
				return false, err
			}
		}
	}
	return wrote, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// SecurityTimeline implements Events.
func (db *DB) SecurityTimeline(ctx context.Context, ticker string, knownAt time.Time, limit int) ([]TimelineEntry, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	if knownAt.IsZero() {
		knownAt = db.now()
	}
	if limit <= 0 {
		limit = 50
	}
	known := ms(knownAt)
	rows, err := h.r.QueryContext(ctx, `SELECT r.event_id, r.revision, r.kind, r.status, r.title, es.role, r.public_ms, r.recorded_ms,
			r.materiality, r.relevance, (SELECT COUNT(*) FROM event_evidence v WHERE v.event_id = r.event_id AND v.revision = r.revision)
		FROM security_ticker t
		JOIN event_security es ON es.security_id = t.security_id
		JOIN event_revision r ON r.event_id = es.event_id AND r.revision = es.revision
		WHERE t.ticker = ? AND r.recorded_ms <= ? AND r.public_ms <= ?
		  AND r.revision = (SELECT MAX(r2.revision) FROM event_revision r2 WHERE r2.event_id = r.event_id AND r2.recorded_ms <= ?)
		GROUP BY r.event_id
		ORDER BY r.public_ms DESC, r.event_id LIMIT ?`, strings.ToUpper(strings.TrimSpace(ticker)), known, known, known, limit)
	if err != nil {
		return nil, fmt.Errorf("store: timeline: %w", err)
	}
	defer rows.Close()
	var out []TimelineEntry
	for rows.Next() {
		var e TimelineEntry
		var kind, role string
		var pub, rec int64
		if err := rows.Scan(&e.EventID, &e.Revision, &kind, &e.Status, &e.Title, &role, &pub, &rec, &e.Materiality, &e.Relevance, &e.Evidence); err != nil {
			return nil, fmt.Errorf("store: timeline: %w", err)
		}
		e.Kind, e.Role, e.PublicAt, e.RecordedAt = model.EventKind(kind), model.Role(role), fromMS(pub), fromMS(rec)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventHistory implements Events.
func (db *DB) EventHistory(ctx context.Context, eventID string) ([]model.EventRevision, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT revision, kind, status, title, effective_day, public_ms, recorded_ms, materiality, relevance, lens_version, supersedes
		FROM event_revision WHERE event_id = ? ORDER BY revision`, eventID)
	if err != nil {
		return nil, fmt.Errorf("store: event history: %w", err)
	}
	var out []model.EventRevision
	for rows.Next() {
		r := model.EventRevision{EventID: eventID}
		var kind string
		var pub, rec int64
		if err := rows.Scan(&r.Revision, &kind, &r.Status, &r.Title, &r.EffectiveDay, &pub, &rec, &r.Materiality, &r.Relevance, &r.LensVersion, &r.Supersedes); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: event history: %w", err)
		}
		r.Kind, r.PublicAt, r.RecordedAt = model.EventKind(kind), fromMS(pub), fromMS(rec)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		r := &out[i]
		srows, err := h.r.QueryContext(ctx, `SELECT s.security_id, COALESCE((SELECT t.ticker FROM security_ticker t WHERE t.security_id = s.security_id
				ORDER BY t.to_day = '' DESC, t.from_day DESC LIMIT 1), ''), s.role FROM event_security s
			WHERE s.event_id = ? AND s.revision = ? ORDER BY s.security_id`, eventID, r.Revision)
		if err != nil {
			return nil, fmt.Errorf("store: event history: %w", err)
		}
		for srows.Next() {
			var es model.EventSecurity
			var role string
			if err := srows.Scan(&es.SecurityID, &es.Ticker, &role); err != nil {
				srows.Close()
				return nil, err
			}
			es.Role = model.Role(role)
			r.Securities = append(r.Securities, es)
		}
		srows.Close()
		erows, err := h.r.QueryContext(ctx, `SELECT article_id, source_id FROM event_evidence WHERE event_id = ? AND revision = ? ORDER BY article_id, source_id`, eventID, r.Revision)
		if err != nil {
			return nil, err
		}
		for erows.Next() {
			var ev model.Evidence
			if err := erows.Scan(&ev.ArticleID, &ev.SourceID); err != nil {
				erows.Close()
				return nil, err
			}
			r.Evidence = append(r.Evidence, ev)
		}
		erows.Close()
		trows, err := h.r.QueryContext(ctx, `SELECT topic FROM event_topic WHERE event_id = ? AND revision = ? ORDER BY topic`, eventID, r.Revision)
		if err != nil {
			return nil, err
		}
		for trows.Next() {
			var t string
			if err := trows.Scan(&t); err != nil {
				trows.Close()
				return nil, err
			}
			r.Topics = append(r.Topics, t)
		}
		trows.Close()
		arows, err := h.r.QueryContext(ctx, `SELECT security_id, rule, effect, direction, horizon, confidence, rationale, assessed_ms
			FROM event_assessment WHERE event_id = ? AND revision = ? ORDER BY security_id, rule, effect`, eventID, r.Revision)
		if err != nil {
			return nil, err
		}
		for arows.Next() {
			var a model.Assessment
			var at int64
			if err := arows.Scan(&a.SecurityID, &a.Rule, &a.Effect, &a.Direction, &a.Horizon, &a.Confidence, &a.Rationale, &at); err != nil {
				arows.Close()
				return nil, err
			}
			a.At = fromMS(at)
			r.Assessments = append(r.Assessments, a)
		}
		arows.Close()
	}
	return out, nil
}
