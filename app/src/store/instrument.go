package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/instrument"
)

// InstrumentsAsOf returns the issuers, securities and listings as this system
// knew them at t (zero: now): the newest revision of each recorded by then.
func (db *DB) InstrumentsAsOf(ctx context.Context, t time.Time) (instrument.Set, error) {
	h, err := db.baseHandle()
	if err != nil {
		return instrument.Set{}, err
	}
	return loadInstruments(ctx, h.r, t)
}

type queryer interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

func loadInstruments(ctx context.Context, q queryer, t time.Time) (instrument.Set, error) {
	bound := int64(1) << 62
	if !t.IsZero() {
		bound = ms(t)
	}
	var set instrument.Set

	rows, err := q.QueryContext(ctx, `SELECT r.issuer_id, r.name, r.country, r.cik, r.lei FROM issuer_revision r
		WHERE r.revision = (SELECT MAX(revision) FROM issuer_revision WHERE issuer_id = r.issuer_id AND recorded_ms <= ?) ORDER BY r.issuer_id`, bound)
	if err != nil {
		return set, fmt.Errorf("store: instruments: %w", err)
	}
	for rows.Next() {
		var i instrument.Issuer
		if err := rows.Scan(&i.ID, &i.Name, &i.Country, &i.CIK, &i.LEI); err != nil {
			rows.Close()
			return set, fmt.Errorf("store: instruments: %w", err)
		}
		set.Issuers = append(set.Issuers, i)
	}
	if err := rows.Close(); err != nil {
		return set, err
	}

	rows, err = q.QueryContext(ctx, `SELECT r.security_id, r.issuer_id, r.name, r.kind, r.share_class, r.isin, r.distribution, r.domicile, r.ucits FROM security_revision r
		WHERE r.revision = (SELECT MAX(revision) FROM security_revision WHERE security_id = r.security_id AND recorded_ms <= ?) ORDER BY r.security_id`, bound)
	if err != nil {
		return set, fmt.Errorf("store: instruments: %w", err)
	}
	for rows.Next() {
		var s instrument.Security
		var kind, dist, ucits string
		if err := rows.Scan(&s.ID, &s.IssuerID, &s.Name, &kind, &s.ShareClass, &s.ISIN, &dist, &s.Domicile, &ucits); err != nil {
			rows.Close()
			return set, fmt.Errorf("store: instruments: %w", err)
		}
		s.Kind, s.Distribution, s.UCITS = instrument.Kind(kind), instrument.Distribution(dist), instrument.Tri(ucits)
		set.Securities = append(set.Securities, s)
	}
	if err := rows.Close(); err != nil {
		return set, err
	}

	rows, err = q.QueryContext(ctx, `SELECT r.listing_id, r.security_id, r.mic, r.ticker, r.currency, r.quote_unit, r.from_day, r.to_day, r.primary_flag, r.contracts FROM listing_revision r
		WHERE r.revision = (SELECT MAX(revision) FROM listing_revision WHERE listing_id = r.listing_id AND recorded_ms <= ?) ORDER BY r.listing_id`, bound)
	if err != nil {
		return set, fmt.Errorf("store: instruments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l instrument.Listing
		var prim, contracts string
		if err := rows.Scan(&l.ID, &l.SecurityID, &l.MIC, &l.Ticker, &l.Currency, &l.QuoteUnit, &l.From, &l.To, &prim, &contracts); err != nil {
			return set, fmt.Errorf("store: instruments: %w", err)
		}
		l.Primary = instrument.Tri(prim)
		if err := json.Unmarshal([]byte(contracts), &l.Contracts); err != nil {
			return set, fmt.Errorf("store: instruments: listing %s: %w", l.ID, err)
		}
		set.Listings = append(set.Listings, l)
	}
	return set, rows.Err()
}

// PutInstruments imports instruments as a new revision of each record that
// differs from the stored one. The whole merged set is validated first and any
// error stops the import with nothing written; a record left out of the file is
// kept, and a record identical to the stored one adds no row. A security that
// names LinkTicker takes the ID of the security that carries that ticker in the
// evidence history, and the listings that named it are pointed there, so events
// already recorded keep their identity.
func (db *DB) PutInstruments(ctx context.Context, in instrument.Set, source string, at time.Time) (instrument.ImportReport, error) {
	var rep instrument.ImportReport
	if strings.TrimSpace(source) == "" {
		return rep, errors.New("store: instruments: a source is required")
	}
	h, err := db.baseHandle()
	if err != nil {
		return rep, err
	}
	if at.IsZero() {
		at = db.now()
	}
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return rep, fmt.Errorf("store: instruments: %w", err)
	}
	defer tx.Rollback()

	// Resolve the identity of each incoming security.
	remap := map[string]string{}
	in = instrument.Set{Issuers: in.Issuers, Securities: append([]instrument.Security(nil), in.Securities...), Listings: append([]instrument.Listing(nil), in.Listings...)}
	for i, s := range in.Securities {
		handle := s.ID
		final := handle
		if s.LinkTicker != "" {
			var id string
			err := tx.QueryRowContext(ctx, `SELECT security_id FROM security_ticker WHERE ticker = ? AND to_day = '' ORDER BY from_day DESC LIMIT 1`,
				strings.ToUpper(strings.TrimSpace(s.LinkTicker))).Scan(&id)
			switch {
			case err == nil:
				if handle != "" && handle != id && strings.HasPrefix(handle, "sec_") && existsSecurity(ctx, tx, handle) {
					return rep, fmt.Errorf("store: instruments: %s names the ticker %s, which is the security %s, but %s already exists", handle, s.LinkTicker, id, handle)
				}
				final = id
				rep.Linked = append(rep.Linked, fmt.Sprintf("%s -> %s via %s", handle, id, s.LinkTicker))
			case errors.Is(err, sql.ErrNoRows):
				// Nothing in the history carries the ticker: the handle becomes the ID.
			default:
				return rep, fmt.Errorf("store: instruments: %w", err)
			}
		}
		if final == "" {
			if s.ISIN == "" {
				return rep, fmt.Errorf("store: instruments: security %q needs an id or an ISIN", s.Name)
			}
			sum := sha256.Sum256([]byte("isin:" + s.ISIN))
			final = "sec_" + hex.EncodeToString(sum[:6])
		}
		if handle != "" {
			remap[handle] = final
		}
		in.Securities[i].ID = final
		in.Securities[i].LinkTicker = ""
	}
	for i, l := range in.Listings {
		if to, ok := remap[l.SecurityID]; ok {
			in.Listings[i].SecurityID = to
		}
	}

	existing, err := loadInstruments(ctx, tx, time.Time{})
	if err != nil {
		return rep, err
	}
	merged := instrument.Merge(existing, instrument.Normalize(in))
	problems := instrument.Validate(merged)
	if errs := instrument.Errors(problems); len(errs) > 0 {
		lines := make([]string, len(errs))
		for i, p := range errs {
			lines[i] = p.String()
		}
		return rep, fmt.Errorf("store: instruments: not imported:\n  %s", strings.Join(lines, "\n  "))
	}
	rep.Problems = problems
	norm := instrument.Normalize(in)

	for _, i := range norm.Issuers {
		n, err := putRevision(ctx, tx, "issuer_revision", "issuer_id", i.ID, i,
			`INSERT INTO issuer_revision (issuer_id, revision, name, country, cik, lei, source, recorded_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{i.Name, i.Country, i.CIK, i.LEI}, source, at, &rep.Issuers)
		if err != nil {
			return rep, err
		}
		_ = n
	}
	for _, s := range norm.Securities {
		cik := ""
		for _, i := range merged.Issuers {
			if i.ID == s.IssuerID {
				cik = i.CIK
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO security (id, cik, name, class, created_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, s.ID, cik, s.Name, s.ShareClass, ms(at)); err != nil {
			return rep, fmt.Errorf("store: instruments: %w", err)
		}
		if _, err := putRevision(ctx, tx, "security_revision", "security_id", s.ID, s,
			`INSERT INTO security_revision (security_id, revision, issuer_id, name, kind, share_class, isin, distribution, domicile, ucits, source, recorded_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{s.IssuerID, s.Name, string(s.Kind), s.ShareClass, s.ISIN, string(s.Distribution), s.Domicile, string(s.UCITS)}, source, at, &rep.Securities); err != nil {
			return rep, err
		}
	}
	for _, l := range norm.Listings {
		contracts, _ := json.Marshal(nonNilContracts(l.Contracts))
		if _, err := putRevision(ctx, tx, "listing_revision", "listing_id", l.ID, l,
			`INSERT INTO listing_revision (listing_id, revision, security_id, mic, ticker, currency, quote_unit, from_day, to_day, primary_flag, contracts, source, recorded_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{l.SecurityID, l.MIC, l.Ticker, l.Currency, l.QuoteUnit, l.From, l.To, string(l.Primary), string(contracts)}, source, at, &rep.Listings); err != nil {
			return rep, err
		}
	}
	if err := tx.Commit(); err != nil {
		return rep, fmt.Errorf("store: instruments: %w", err)
	}
	return rep, nil
}

func existsSecurity(ctx context.Context, tx *sql.Tx, id string) bool {
	var n int
	_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM security WHERE id = ?`, id).Scan(&n)
	return n > 0
}

func nonNilContracts(c []instrument.Contract) []instrument.Contract {
	if c == nil {
		return []instrument.Contract{}
	}
	return c
}

// putRevision appends a revision of one record unless the newest stored one has
// the same content. The content is compared as the JSON of the record, with the
// listing's contracts and every field included.
func putRevision(ctx context.Context, tx *sql.Tx, table, idCol, id string, rec any, insert string, cols []any, source string, at time.Time, c *instrument.Counts) (int, error) {
	var rev int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) FROM `+table+` WHERE `+idCol+` = ?`, id).Scan(&rev)
	if err != nil {
		return 0, fmt.Errorf("store: instruments: %w", err)
	}
	if rev > 0 {
		prev, err := latestJSON(ctx, tx, table, idCol, id, rev)
		if err != nil {
			return 0, err
		}
		cur, _ := json.Marshal(rec)
		if prev == string(cur) {
			c.Unchanged++
			return rev, nil
		}
		c.Revised++
	} else {
		c.New++
	}
	args := append([]any{id, rev + 1}, cols...)
	args = append(args, source, ms(at))
	if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
		return 0, fmt.Errorf("store: instruments: %w", err)
	}
	return rev + 1, nil
}

// latestJSON rebuilds the stored record of a revision as the same JSON the
// importer compares against.
func latestJSON(ctx context.Context, tx *sql.Tx, table, idCol, id string, rev int) (string, error) {
	set, err := loadInstrumentRevision(ctx, tx, table, idCol, id, rev)
	if err != nil {
		return "", err
	}
	return set, nil
}

func loadInstrumentRevision(ctx context.Context, tx *sql.Tx, table, idCol, id string, rev int) (string, error) {
	switch table {
	case "issuer_revision":
		var i instrument.Issuer
		err := tx.QueryRowContext(ctx, `SELECT issuer_id, name, country, cik, lei FROM issuer_revision WHERE issuer_id = ? AND revision = ?`, id, rev).Scan(&i.ID, &i.Name, &i.Country, &i.CIK, &i.LEI)
		if err != nil {
			return "", fmt.Errorf("store: instruments: %w", err)
		}
		b, _ := json.Marshal(i)
		return string(b), nil
	case "security_revision":
		var s instrument.Security
		var kind, dist, ucits string
		err := tx.QueryRowContext(ctx, `SELECT security_id, issuer_id, name, kind, share_class, isin, distribution, domicile, ucits FROM security_revision WHERE security_id = ? AND revision = ?`, id, rev).
			Scan(&s.ID, &s.IssuerID, &s.Name, &kind, &s.ShareClass, &s.ISIN, &dist, &s.Domicile, &ucits)
		if err != nil {
			return "", fmt.Errorf("store: instruments: %w", err)
		}
		s.Kind, s.Distribution, s.UCITS = instrument.Kind(kind), instrument.Distribution(dist), instrument.Tri(ucits)
		b, _ := json.Marshal(s)
		return string(b), nil
	default:
		var l instrument.Listing
		var prim, contracts string
		err := tx.QueryRowContext(ctx, `SELECT listing_id, security_id, mic, ticker, currency, quote_unit, from_day, to_day, primary_flag, contracts FROM listing_revision WHERE listing_id = ? AND revision = ?`, id, rev).
			Scan(&l.ID, &l.SecurityID, &l.MIC, &l.Ticker, &l.Currency, &l.QuoteUnit, &l.From, &l.To, &prim, &contracts)
		if err != nil {
			return "", fmt.Errorf("store: instruments: %w", err)
		}
		l.Primary = instrument.Tri(prim)
		if err := json.Unmarshal([]byte(contracts), &l.Contracts); err != nil {
			return "", err
		}
		b, _ := json.Marshal(l)
		return string(b), nil
	}
}
