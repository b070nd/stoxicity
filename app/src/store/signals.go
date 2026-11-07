package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SignalEvent is one event as the signal rules read it: the latest revision of
// an event that a security is the subject of, with the publishers and sources of
// the evidence it rests on.
type SignalEvent struct {
	SecurityID, Ticker string
	EventID            string
	Kind               string
	PublicAt           time.Time
	Materiality        int
	Domains            []string
	Sources            []string
}

// SignalEvents returns the events of at least minMateriality made public in the
// window, one per event and subject security, oldest first.
func (db *DB) SignalEvents(ctx context.Context, from, to time.Time, minMateriality int) ([]SignalEvent, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `
		WITH latest AS (
			SELECT r.event_id, r.revision, r.kind, r.public_ms, r.materiality FROM event_revision r
			WHERE r.public_ms >= ? AND r.public_ms < ? AND r.materiality >= ?
			  AND r.revision = (SELECT MAX(r2.revision) FROM event_revision r2 WHERE r2.event_id = r.event_id))
		SELECT es.security_id,
		       COALESCE((SELECT t.ticker FROM security_ticker t WHERE t.security_id = es.security_id ORDER BY t.to_day = '' DESC, t.from_day DESC LIMIT 1), ''),
		       l.event_id, l.kind, l.public_ms, l.materiality,
		       COALESCE(group_concat(DISTINCT a.domain), ''), COALESCE(group_concat(DISTINCT v.source_id), '')
		FROM latest l
		JOIN event_security es ON es.event_id = l.event_id AND es.revision = l.revision AND es.role = 'subject'
		LEFT JOIN event_evidence v ON v.event_id = l.event_id AND v.revision = l.revision AND v.relation = 'supports'
		LEFT JOIN articles a ON a.id = v.article_id
		GROUP BY es.security_id, l.event_id ORDER BY es.security_id, l.public_ms, l.event_id`, ms(from), ms(to), minMateriality)
	if err != nil {
		return nil, fmt.Errorf("store: signals: %w", err)
	}
	defer rows.Close()
	var out []SignalEvent
	for rows.Next() {
		var e SignalEvent
		var pub int64
		var domains, sources string
		if err := rows.Scan(&e.SecurityID, &e.Ticker, &e.EventID, &e.Kind, &pub, &e.Materiality, &domains, &sources); err != nil {
			return nil, fmt.Errorf("store: signals: %w", err)
		}
		e.PublicAt = fromMS(pub)
		e.Domains, e.Sources = splitList(domains), splitList(sources)
		out = append(out, e)
	}
	return out, rows.Err()
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// SecurityInfo is a security as it stands now.
type SecurityInfo struct {
	ID, CIK, Name, Class, Ticker string
}

// SecurityInfoOf returns the security that carries the ticker now, or the one
// that carried it last.
func (db *DB) SecurityInfoOf(ctx context.Context, ticker string) (SecurityInfo, bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return SecurityInfo{}, false, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT s.id, s.cik, s.name, s.class, t.ticker FROM security_ticker t JOIN security s ON s.id = t.security_id
		WHERE t.ticker = ? ORDER BY t.to_day = '' DESC, t.from_day DESC LIMIT 1`, normTickerUpper(ticker))
	if err != nil {
		return SecurityInfo{}, false, fmt.Errorf("store: security: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return SecurityInfo{}, false, rows.Err()
	}
	var s SecurityInfo
	if err := rows.Scan(&s.ID, &s.CIK, &s.Name, &s.Class, &s.Ticker); err != nil {
		return SecurityInfo{}, false, fmt.Errorf("store: security: %w", err)
	}
	return s, true, nil
}

// SecuritiesWithEvents lists the securities that any event made public since
// the time concerns (in any role), by ticker.
func (db *DB) SecuritiesWithEvents(ctx context.Context, since time.Time) ([]SecurityInfo, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT s.id, s.cik, s.name, s.class,
			COALESCE((SELECT t.ticker FROM security_ticker t WHERE t.security_id = s.id ORDER BY t.to_day = '' DESC, t.from_day DESC LIMIT 1), '')
		FROM security s WHERE EXISTS (SELECT 1 FROM event_security es JOIN event_revision r ON r.event_id = es.event_id AND r.revision = es.revision
			WHERE es.security_id = s.id AND r.public_ms >= ?) ORDER BY 5, s.id`, ms(since))
	if err != nil {
		return nil, fmt.Errorf("store: securities: %w", err)
	}
	defer rows.Close()
	var out []SecurityInfo
	for rows.Next() {
		var s SecurityInfo
		if err := rows.Scan(&s.ID, &s.CIK, &s.Name, &s.Class, &s.Ticker); err != nil {
			return nil, fmt.Errorf("store: securities: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func normTickerUpper(t string) string { return strings.ToUpper(strings.TrimSpace(t)) }
