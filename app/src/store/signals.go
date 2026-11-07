package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/origin"
)

// SignalEvidence is one article an event rests on, with what the episode and
// origin rules read of it.
type SignalEvidence struct {
	ArticleID, SourceID, Domain, Form, CIK, Title, URL string
	Items                                              []string
	Published                                          time.Time
}

// SignalEvent is one event as the signal rules read it: the latest revision of
// an event that a security is the subject of, with the evidence it rests on.
type SignalEvent struct {
	SecurityID, Ticker string
	EventID            string
	Kind               string
	Title              string
	EffectiveDay       string
	PublicAt           time.Time
	Materiality        int
	// Counterparties are the securities the event names in that role.
	Counterparties []string
	// Evidence are the supporting articles. Domains and Sources are the distinct
	// publishers and sources among them.
	Evidence []SignalEvidence
	Domains  []string
	Sources  []string
	// Origins are the reporting origins of the evidence by article ID, as
	// understood at the query's cutoff (see AttachOrigins).
	Origins map[string]origin.Origin
}

// SignalQuery says which events SignalEvents returns.
type SignalQuery struct {
	From, To time.Time
	// MinMateriality keeps only events of at least this materiality. Zero keeps
	// every event: materiality is a keyword rule's reading and takes no part in
	// whether evidence is corroborated, so the default is not to filter by it.
	MinMateriality int
	// AsOf reads the history as it was recorded at that time: revisions
	// recorded later are not seen. Zero reads the history as it stands.
	AsOf time.Time
}

// SignalEvents returns the events made public in the window, one per event and
// subject security, oldest first.
func (db *DB) SignalEvents(ctx context.Context, q SignalQuery) ([]SignalEvent, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !q.AsOf.IsZero() {
		bound = ms(q.AsOf)
	}
	const latest = `WITH latest AS (
				SELECT r.event_id, r.revision, r.kind, r.title, r.effective_day, r.public_ms, r.materiality FROM event_revision r
				WHERE r.public_ms >= ? AND r.public_ms < ? AND r.materiality >= ? AND r.recorded_ms <= ? AND r.status <> 'withdrawn'
				  AND r.revision = (SELECT MAX(r2.revision) FROM event_revision r2 WHERE r2.event_id = r.event_id AND r2.recorded_ms <= ?))`
	args := []any{ms(q.From), ms(q.To), q.MinMateriality, bound, bound}
	rows, err := h.r.QueryContext(ctx, latest+`
			SELECT es.security_id,
			       COALESCE((SELECT t.ticker FROM security_ticker t WHERE t.security_id = es.security_id ORDER BY t.to_day = '' DESC, t.from_day DESC LIMIT 1), ''),
			       l.event_id, l.kind, l.title, l.effective_day, l.public_ms, l.materiality,
			       COALESCE((SELECT group_concat(c.security_id) FROM event_security c WHERE c.event_id = l.event_id AND c.revision = l.revision AND c.role = 'counterparty'), '')
			FROM latest l
			JOIN event_security es ON es.event_id = l.event_id AND es.revision = l.revision AND es.role = 'subject'
			ORDER BY es.security_id, l.public_ms, l.event_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: signals: %w", err)
	}
	var out []SignalEvent
	index := map[string][]int{} // event ID -> positions in out
	for rows.Next() {
		var e SignalEvent
		var pub int64
		var counter string
		if err := rows.Scan(&e.SecurityID, &e.Ticker, &e.EventID, &e.Kind, &e.Title, &e.EffectiveDay, &pub, &e.Materiality, &counter); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: signals: %w", err)
		}
		e.PublicAt = fromMS(pub)
		e.Counterparties = splitList(counter)
		index[e.EventID] = append(index[e.EventID], len(out))
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: signals: %w", err)
	}
	rows.Close()

	ev, err := h.r.QueryContext(ctx, latest+`
			SELECT l.event_id, v.article_id, v.source_id, a.domain, a.form, a.cik, a.title, a.url, a.items, a.published_ms
			FROM latest l
			JOIN event_evidence v ON v.event_id = l.event_id AND v.revision = l.revision AND v.relation = 'supports'
			JOIN articles a ON a.id = v.article_id
			ORDER BY l.event_id, a.published_ms, v.article_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: signals: %w", err)
	}
	defer ev.Close()
	for ev.Next() {
		var id, items string
		var x SignalEvidence
		var pub int64
		if err := ev.Scan(&id, &x.ArticleID, &x.SourceID, &x.Domain, &x.Form, &x.CIK, &x.Title, &x.URL, &items, &pub); err != nil {
			return nil, fmt.Errorf("store: signals: %w", err)
		}
		x.Published = fromMS(pub)
		_ = json.Unmarshal([]byte(items), &x.Items)
		for _, i := range index[id] {
			out[i].Evidence = append(out[i].Evidence, x)
		}
	}
	if err := ev.Err(); err != nil {
		return nil, fmt.Errorf("store: signals: %w", err)
	}
	for i := range out {
		seenD, seenS := map[string]bool{}, map[string]bool{}
		for _, x := range out[i].Evidence {
			if x.Domain != "" && !seenD[x.Domain] {
				seenD[x.Domain] = true
				out[i].Domains = append(out[i].Domains, x.Domain)
			}
			if !seenS[x.SourceID] {
				seenS[x.SourceID] = true
				out[i].Sources = append(out[i].Sources, x.SourceID)
			}
		}
	}
	return out, nil
}

// AttachOrigins reads the origins of the events' evidence, as understood at
// asOf (zero: now), and sets them on the events. An article without an origin
// is left out of the map, which means its origin has not been assessed.
func (db *DB) AttachOrigins(ctx context.Context, events []SignalEvent, asOf time.Time) error {
	seen := map[string]bool{}
	var ids []string
	for _, e := range events {
		for _, x := range e.Evidence {
			if !seen[x.ArticleID] {
				seen[x.ArticleID] = true
				ids = append(ids, x.ArticleID)
			}
		}
	}
	os, err := db.Origins(ctx, ids, asOf)
	if err != nil {
		return err
	}
	for i := range events {
		events[i].Origins = map[string]origin.Origin{}
		for _, x := range events[i].Evidence {
			if o, ok := os[x.ArticleID]; ok {
				events[i].Origins[x.ArticleID] = o
			}
		}
	}
	return nil
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
