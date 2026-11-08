package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/quotes"
)

// ObsCounts says how many observations an import stored and how many the store
// already held in exactly this form.
type ObsCounts struct {
	New       int `json:"new"`
	Unchanged int `json:"unchanged"`
}

// PutFX appends exchange-rate observations. An observation identical to one the
// store already holds from the same source (pair, day, basis, value) adds
// nothing; a different value from the same source is a revision and is kept
// beside the old one. Every row is validated first and any failure stores none.
// Received is set to at when the observation has none.
func (db *DB) PutFX(ctx context.Context, obs []quotes.FXObs, at time.Time) (ObsCounts, error) {
	var c ObsCounts
	for _, o := range obs {
		if err := o.Validate(); err != nil {
			return c, err
		}
	}
	h, err := db.baseHandle()
	if err != nil {
		return c, err
	}
	if at.IsZero() {
		at = db.now()
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return c, fmt.Errorf("store: fx: %w", err)
	}
	defer tx.Rollback()
	for _, o := range obs {
		rec := o.Received
		if rec.IsZero() {
			rec = at
		}
		// A repeat of the newest value from this source for this day adds nothing;
		// a return to an older value after a correction is a new revision.
		var last string
		err := tx.QueryRowContext(ctx, `SELECT value FROM fx_obs WHERE base = ? AND quote = ? AND day = ? AND basis = ? AND source = ? ORDER BY seq DESC LIMIT 1`,
			string(o.Base), string(o.Quote), o.Day, string(o.Basis), o.Source).Scan(&last)
		if err == nil && last == o.Value.String() {
			c.Unchanged++
			continue
		}
		if err != nil && err != sql.ErrNoRows {
			return c, fmt.Errorf("store: fx: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fx_obs (base, quote, value, basis, day, source, source_ref, received_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			string(o.Base), string(o.Quote), o.Value.String(), string(o.Basis), o.Day, o.Source, o.SourceRef, ms(rec)); err != nil {
			return c, fmt.Errorf("store: fx: %w", err)
		}
		c.New++
	}
	if err := tx.Commit(); err != nil {
		return c, fmt.Errorf("store: fx: %w", err)
	}
	return c, nil
}

// FXObservations returns every observation of the pair, in either direction,
// received by knownAt (zero: all), oldest first.
func (db *DB) FXObservations(ctx context.Context, a, b money.Currency, knownAt time.Time) ([]quotes.FXObs, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !knownAt.IsZero() {
		bound = ms(knownAt)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT seq, base, quote, value, basis, day, source, source_ref, received_ms FROM fx_obs
		WHERE ((base = ? AND quote = ?) OR (base = ? AND quote = ?)) AND received_ms <= ? ORDER BY day, seq`, string(a), string(b), string(b), string(a), bound)
	if err != nil {
		return nil, fmt.Errorf("store: fx: %w", err)
	}
	defer rows.Close()
	var out []quotes.FXObs
	for rows.Next() {
		var o quotes.FXObs
		var base, quote, val, basis string
		var rec int64
		if err := rows.Scan(&o.Seq, &base, &quote, &val, &basis, &o.Day, &o.Source, &o.SourceRef, &rec); err != nil {
			return nil, fmt.Errorf("store: fx: %w", err)
		}
		v, err := money.ParseDecimal(val)
		if err != nil {
			return nil, fmt.Errorf("store: fx: stored rate %q: %w", val, err)
		}
		o.Base, o.Quote, o.Value, o.Basis, o.Received = money.Currency(base), money.Currency(quote), v, money.Basis(basis), time.UnixMilli(rec).UTC()
		out = append(out, o)
	}
	return out, rows.Err()
}

// FXSummary is one pair as stored: how many observations, which days, which sources.
type FXSummary struct {
	Base, Quote money.Currency
	Basis       money.Basis
	Source      string
	Count       int
	First, Last string
}

// FXSummaries lists what is stored, one row per pair, basis and source.
func (db *DB) FXSummaries(ctx context.Context) ([]FXSummary, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT base, quote, basis, source, COUNT(*), MIN(day), MAX(day) FROM fx_obs GROUP BY base, quote, basis, source ORDER BY base, quote, basis, source`)
	if err != nil {
		return nil, fmt.Errorf("store: fx: %w", err)
	}
	defer rows.Close()
	var out []FXSummary
	for rows.Next() {
		var s FXSummary
		var b, q, basis string
		if err := rows.Scan(&b, &q, &basis, &s.Source, &s.Count, &s.First, &s.Last); err != nil {
			return nil, fmt.Errorf("store: fx: %w", err)
		}
		s.Base, s.Quote, s.Basis = money.Currency(b), money.Currency(q), money.Basis(basis)
		out = append(out, s)
	}
	return out, rows.Err()
}

// PutPrices appends price observations with the same rules as PutFX: validated
// whole, identical repeats add nothing, a different value is a revision.
func (db *DB) PutPrices(ctx context.Context, obs []quotes.PriceObs, at time.Time) (ObsCounts, error) {
	var c ObsCounts
	for _, o := range obs {
		if err := o.Validate(); err != nil {
			return c, err
		}
	}
	h, err := db.baseHandle()
	if err != nil {
		return c, err
	}
	if at.IsZero() {
		at = db.now()
	}
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return c, fmt.Errorf("store: prices: %w", err)
	}
	defer tx.Rollback()
	for _, o := range obs {
		rec := o.Received
		if rec.IsZero() {
			rec = at
		}
		var last, lastUnit string
		err := tx.QueryRowContext(ctx, `SELECT value, unit FROM price_obs WHERE listing_id = ? AND day = ? AND kind = ? AND adjustment = ? AND source = ? ORDER BY seq DESC LIMIT 1`,
			o.ListingID, o.Day, string(o.Kind), string(o.Adjustment), o.Source).Scan(&last, &lastUnit)
		if err == nil && last == o.Price.Value.String() && lastUnit == o.Price.Unit.Code {
			c.Unchanged++
			continue
		}
		if err != nil && err != sql.ErrNoRows {
			return c, fmt.Errorf("store: prices: %w", err)
		}
		var q int64
		if !o.QuoteTime.IsZero() {
			q = ms(o.QuoteTime)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO price_obs (listing_id, day, kind, value, unit, unit_basis, adjustment, delay, quote_ms, source, source_ref, received_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			o.ListingID, o.Day, string(o.Kind), o.Price.Value.String(), o.Price.Unit.Code, string(o.UnitBasis), string(o.Adjustment), string(o.Delay), q, o.Source, o.SourceRef, ms(rec)); err != nil {
			return c, fmt.Errorf("store: prices: %w", err)
		}
		c.New++
	}
	if err := tx.Commit(); err != nil {
		return c, fmt.Errorf("store: prices: %w", err)
	}
	return c, nil
}

// PriceObservations returns every observation of the listing received by
// knownAt (zero: all), oldest first.
func (db *DB) PriceObservations(ctx context.Context, listingID string, knownAt time.Time) ([]quotes.PriceObs, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	bound := int64(1) << 62
	if !knownAt.IsZero() {
		bound = ms(knownAt)
	}
	rows, err := h.r.QueryContext(ctx, `SELECT seq, listing_id, day, kind, value, unit, unit_basis, adjustment, delay, quote_ms, source, source_ref, received_ms FROM price_obs
		WHERE listing_id = ? AND received_ms <= ? ORDER BY day, seq`, listingID, bound)
	if err != nil {
		return nil, fmt.Errorf("store: prices: %w", err)
	}
	defer rows.Close()
	var out []quotes.PriceObs
	for rows.Next() {
		var o quotes.PriceObs
		var kind, val, unit, ub, adj, delay string
		var q, rec int64
		if err := rows.Scan(&o.Seq, &o.ListingID, &o.Day, &kind, &val, &unit, &ub, &adj, &delay, &q, &o.Source, &o.SourceRef, &rec); err != nil {
			return nil, fmt.Errorf("store: prices: %w", err)
		}
		v, err := money.ParseDecimal(val)
		if err != nil {
			return nil, fmt.Errorf("store: prices: stored price %q: %w", val, err)
		}
		u, err := money.ParseQuoteUnit(unit)
		if err != nil {
			return nil, fmt.Errorf("store: prices: %w", err)
		}
		o.Kind, o.Price, o.UnitBasis, o.Adjustment, o.Delay = quotes.Kind(kind), money.Price{Value: v, Unit: u}, quotes.UnitBasis(ub), quotes.Adjustment(adj), quotes.Delay(delay)
		o.Received = time.UnixMilli(rec).UTC()
		if q != 0 {
			o.QuoteTime = time.UnixMilli(q).UTC()
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
