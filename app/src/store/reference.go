package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Reference is the reference database: every stock symbol, company and its
// basic data, as the SEC and Nasdaq publish them. It holds the latest content
// of each source and a history of what changed.
//
// Every Replace is one transaction. The rows are compared with what is stored
// and written in key order, and a source whose content hash did not change
// writes nothing but the time it was confirmed, so the database is a function
// of the content of its sources and not of how often they were asked.
type Reference interface {
	// RefSource returns the state of a source, and false if it was never stored.
	RefSource(ctx context.Context, name string) (RefSource, bool, error)
	// RefSources returns every source, by name.
	RefSources(ctx context.Context) ([]RefSource, error)
	// RefConfirm records that a source was asked and its content is current
	// (a 304, or the same content again), or why asking failed (errText).
	RefConfirm(ctx context.Context, name, etag, lastModified string, at time.Time, errText string) error

	// ReplaceTickers makes the SEC ticker list equal to rows.
	ReplaceTickers(ctx context.Context, src RefSource, rows []RefTicker, at time.Time) (RefDiff, error)
	// ReplaceListing makes the Nasdaq directory equal to rows.
	ReplaceListing(ctx context.Context, src RefSource, rows []RefListing, at time.Time) (RefDiff, error)
	// PutCompanies inserts or replaces company records by CIK.
	PutCompanies(ctx context.Context, rows []RefCompany, at time.Time) (RefDiff, error)
	// PutWebsites inserts or replaces the official websites by ticker.
	PutWebsites(ctx context.Context, rows []RefWebsite, at time.Time) (RefDiff, error)

	RefTickers(ctx context.Context) ([]RefTicker, error)                               // by ticker
	RefListings(ctx context.Context) ([]RefListing, error)                             // by symbol
	RefCompanies(ctx context.Context) ([]RefCompany, error)                            // by CIK
	RefWebsites(ctx context.Context) ([]RefWebsite, error)                             // by ticker
	RefSnapshots(ctx context.Context, source string, limit int) ([]RefSnapshot, error) // newest first
	RefChanges(ctx context.Context, since time.Time, limit int) ([]RefChange, error)   // newest first
}

// Source names.
const (
	SourceTickers = "sec-tickers"
	SourceNasdaq  = "nasdaq-listed"
	SourceOther   = "nasdaq-other"
	SourceCompany = "sec-company"
	SourceWebsite = "wikidata-sites"
)

// RefSource is what is known about where a table's content came from.
type RefSource struct {
	Name         string
	ETag         string
	LastModified string
	Hash         string
	Records      int
	CheckedAt    time.Time
	ChangedAt    time.Time
	Error        string
}

// RefTicker is a row of the SEC ticker list.
type RefTicker struct{ Ticker, Name, CIK, Exchange string }

// RefListing is a row of the Nasdaq directory.
type RefListing struct {
	Symbol, Name, Venue, Market, Status string
	ETF, Test                           bool
}

// RefCompany is the SEC record of one company.
type RefCompany struct {
	CIK, Name, SIC, Description, EntityType, Category, State, FiscalYear, Website string
	Former                                                                        []string
	// Exchanges are the markets the SEC says the stock trades on. City and
	// Region are the business address; EIN and LEI identify the company;
	// Investor is its investor-relations website.
	Exchanges                               []string
	City, Region, Phone, EIN, LEI, Investor string
	Missing                                 bool
	FetchedAt                               time.Time
}

// RefWebsite is the official website of one ticker; URL is empty when it was
// looked for and not found.
type RefWebsite struct {
	Ticker, URL, Label string
	FetchedAt          time.Time
}

// companyExtra is the part of a company record kept as JSON in one column.
type companyExtra struct {
	Exchanges []string `json:"exchanges,omitempty"`
	City      string   `json:"city,omitempty"`
	Region    string   `json:"region,omitempty"`
	Phone     string   `json:"phone,omitempty"`
	EIN       string   `json:"ein,omitempty"`
	LEI       string   `json:"lei,omitempty"`
	Investor  string   `json:"investor,omitempty"`
}

func (c RefCompany) extra() string {
	raw, _ := json.Marshal(companyExtra{c.Exchanges, c.City, c.Region, c.Phone, c.EIN, c.LEI, c.Investor})
	return string(raw)
}

// RefDiff says what a Replace or Put did.
type RefDiff struct {
	Added, Removed, Changed int
	// Same is true when the content was the same as before: nothing was written
	// but the confirmation time.
	Same bool
	Hash string
}

// RefSnapshot is one change of a source's content.
type RefSnapshot struct {
	Source  string
	Hash    string
	Records int
	At      time.Time
}

// RefChange is one ticker, symbol or company that appeared, went or changed.
type RefChange struct {
	At     time.Time
	Source string
	Kind   string // added, removed or changed
	Key    string
	Detail string
}

var _ Reference = (*DB)(nil)

// hashRows is the hash of rows already in key order: SHA-256 over one line per
// row, fields separated by a unit separator, so no field can run into the next.
func hashRows[T any](rows []T, line func(T) []string) string {
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(strings.Join(line(r), "\x1f")))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (t RefTicker) fields() []string { return []string{t.Ticker, t.Name, t.CIK, t.Exchange} }
func (l RefListing) fields() []string {
	return []string{l.Symbol, l.Name, l.Venue, l.Market, l.Status, strconv.FormatBool(l.ETF), strconv.FormatBool(l.Test)}
}
func (c RefCompany) fields() []string {
	return []string{c.CIK, c.Name, c.SIC, c.Description, c.EntityType, c.Category, c.State, c.FiscalYear,
		c.Website, strings.Join(c.Former, "\x1e"), strconv.FormatBool(c.Missing), c.extra()}
}

// RefSource implements Reference.
func (db *DB) RefSource(ctx context.Context, name string) (RefSource, bool, error) {
	h, err := db.baseHandle()
	if err != nil {
		return RefSource{}, false, err
	}
	s, err := scanSource(h.r.QueryRowContext(ctx, sourceSelect+` WHERE name = ?`, name))
	if err == sql.ErrNoRows {
		return RefSource{}, false, nil
	}
	return s, err == nil, err
}

const sourceSelect = `SELECT name, etag, last_modified, hash, records, checked_ms, changed_ms, error FROM ref_source`

type scanner interface{ Scan(...any) error }

func scanSource(r scanner) (RefSource, error) {
	var s RefSource
	var checked, changed int64
	if err := r.Scan(&s.Name, &s.ETag, &s.LastModified, &s.Hash, &s.Records, &checked, &changed, &s.Error); err != nil {
		return RefSource{}, err
	}
	if checked != 0 {
		s.CheckedAt = fromMS(checked)
	}
	if changed != 0 {
		s.ChangedAt = fromMS(changed)
	}
	return s, nil
}

// RefSources implements Reference.
func (db *DB) RefSources(ctx context.Context) ([]RefSource, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, sourceSelect+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: reference sources: %w", err)
	}
	defer rows.Close()
	var out []RefSource
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("store: reference sources: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RefConfirm implements Reference.
func (db *DB) RefConfirm(ctx context.Context, name, etag, lastModified string, at time.Time, errText string) error {
	h, err := db.baseHandle()
	if err != nil {
		return err
	}
	// A failure keeps the validators of the last good answer.
	_, err = h.w.ExecContext(ctx, `INSERT INTO ref_source (name, etag, last_modified, checked_ms, error)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET
			etag = CASE WHEN excluded.error = '' THEN excluded.etag ELSE ref_source.etag END,
			last_modified = CASE WHEN excluded.error = '' THEN excluded.last_modified ELSE ref_source.last_modified END,
			checked_ms = CASE WHEN excluded.error = '' THEN excluded.checked_ms ELSE ref_source.checked_ms END,
			error = excluded.error`,
		name, etag, lastModified, ms(at), truncate(errText, 300))
	if err != nil {
		return fmt.Errorf("store: confirm reference source: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// replaceTable is the common part of ReplaceTickers and ReplaceListing: it
// compares the new rows with the stored ones by key, writes the difference and
// records the snapshot and the changes, all in one transaction.
func replaceTable[T any](ctx context.Context, db *DB, src RefSource, at time.Time, rows []T,
	table, keyCol string, key func(T) string, fields func(T) []string,
	load func(ctx context.Context, q querier) ([]T, error),
	insert string, args func(T) []any) (RefDiff, error) {

	h, err := db.baseHandle()
	if err != nil {
		return RefDiff{}, err
	}
	// Canonical order: by key, so the hash does not depend on the answer's order.
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b T) int { return strings.Compare(key(a), key(b)) })
	for i := 1; i < len(rows); i++ {
		if key(rows[i]) == key(rows[i-1]) {
			return RefDiff{}, fmt.Errorf("store: %s: duplicate key %q", table, key(rows[i]))
		}
	}
	hash := hashRows(rows, fields)

	tx, err := h.beginWrite(ctx)
	if err != nil {
		return RefDiff{}, fmt.Errorf("store: %s: %w", table, err)
	}
	defer tx.Rollback()

	old, err := load(ctx, tx)
	if err != nil {
		return RefDiff{}, err
	}
	var stored RefSource
	var haveSource bool
	if s, err := scanSource(tx.QueryRowContext(ctx, sourceSelect+` WHERE name = ?`, src.Name)); err == nil {
		stored, haveSource = s, true
	} else if err != sql.ErrNoRows {
		return RefDiff{}, fmt.Errorf("store: %s: %w", table, err)
	}

	diff := RefDiff{Hash: hash}
	oldHash := hashRows(old, fields) // old is in key order because load sorts it
	if haveSource && stored.Hash == hash && oldHash == hash {
		diff.Same = true
		if _, err := tx.ExecContext(ctx, `UPDATE ref_source SET etag = ?, last_modified = ?, checked_ms = ?, error = '' WHERE name = ?`,
			src.ETag, src.LastModified, ms(at), src.Name); err != nil {
			return RefDiff{}, fmt.Errorf("store: %s: %w", table, err)
		}
		return diff, tx.Commit()
	}

	oldByKey := make(map[string]T, len(old))
	for _, r := range old {
		oldByKey[key(r)] = r
	}
	var changes []RefChange
	newKeys := make(map[string]bool, len(rows))
	for _, r := range rows {
		k := key(r)
		newKeys[k] = true
		o, had := oldByKey[k]
		switch {
		case !had:
			diff.Added++
			changes = append(changes, RefChange{Kind: "added", Key: k, Detail: describe(fields(r))})
		case strings.Join(fields(o), "\x1f") != strings.Join(fields(r), "\x1f"):
			diff.Changed++
			changes = append(changes, RefChange{Kind: "changed", Key: k, Detail: describeChange(fields(o), fields(r))})
		}
	}
	for _, o := range old { // in key order
		if k := key(o); !newKeys[k] {
			diff.Removed++
			changes = append(changes, RefChange{Kind: "removed", Key: k, Detail: describe(fields(o))})
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
		return RefDiff{}, fmt.Errorf("store: %s: %w", table, err)
	}
	stmt, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		return RefDiff{}, fmt.Errorf("store: %s: %w", table, err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, args(r)...); err != nil {
			return RefDiff{}, fmt.Errorf("store: %s: %w", table, err)
		}
	}
	if err := recordChange(ctx, tx, src, hash, len(rows), at, haveSource && len(old) > 0, changes); err != nil {
		return RefDiff{}, err
	}
	return diff, tx.Commit()
}

// recordChange stores the new state of the source, a snapshot, and, unless this
// is the first load, the individual changes (at most maxChanges of them; a
// bigger change is only summarised by the snapshot).
func recordChange(ctx context.Context, tx *sql.Tx, src RefSource, hash string, n int, at time.Time, withChanges bool, changes []RefChange) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ref_source (name, etag, last_modified, hash, records, checked_ms, changed_ms, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, '')
		ON CONFLICT (name) DO UPDATE SET etag = excluded.etag, last_modified = excluded.last_modified,
			hash = excluded.hash, records = excluded.records, checked_ms = excluded.checked_ms,
			changed_ms = excluded.changed_ms, error = ''`,
		src.Name, src.ETag, src.LastModified, hash, n, ms(at), ms(at))
	if err != nil {
		return fmt.Errorf("store: reference source: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ref_snapshot (source, hash, records, at_ms) VALUES (?, ?, ?, ?)`,
		src.Name, hash, n, ms(at)); err != nil {
		return fmt.Errorf("store: reference snapshot: %w", err)
	}
	if !withChanges || len(changes) > maxChanges {
		return nil
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ref_change (at_ms, source, kind, key, detail) VALUES (?, ?, ?, ?, ?)`,
			ms(at), src.Name, c.Kind, c.Key, truncate(c.Detail, 300)); err != nil {
			return fmt.Errorf("store: reference change: %w", err)
		}
	}
	return nil
}

// maxChanges is the largest change that is logged row by row.
const maxChanges = 2000

func describe(f []string) string {
	return strings.Join(slices.DeleteFunc(slices.Clone(f[1:]), func(s string) bool { return s == "" || s == "false" }), " | ")
}

func describeChange(a, b []string) string {
	var out []string
	for i := 1; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			out = append(out, fmt.Sprintf("%q -> %q", a[i], b[i]))
		}
	}
	return strings.Join(out, ", ")
}

// ReplaceTickers implements Reference.
func (db *DB) ReplaceTickers(ctx context.Context, src RefSource, rows []RefTicker, at time.Time) (RefDiff, error) {
	src.Name = SourceTickers
	return replaceTable(ctx, db, src, at, rows, "ref_ticker", "ticker",
		func(t RefTicker) string { return t.Ticker }, RefTicker.fields, loadTickers,
		`INSERT INTO ref_ticker (ticker, name, cik, exchange) VALUES (?, ?, ?, ?)`,
		func(t RefTicker) []any { return []any{t.Ticker, t.Name, t.CIK, t.Exchange} })
}

// ReplaceListing implements Reference.
func (db *DB) ReplaceListing(ctx context.Context, src RefSource, rows []RefListing, at time.Time) (RefDiff, error) {
	if src.Name != SourceOther {
		src.Name = SourceNasdaq // the directory is one table, kept as one source
	}
	return replaceTable(ctx, db, src, at, rows, "ref_listing", "symbol",
		func(l RefListing) string { return l.Symbol }, RefListing.fields, loadListings,
		`INSERT INTO ref_listing (symbol, name, venue, market, status, etf, test) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		func(l RefListing) []any { return []any{l.Symbol, l.Name, l.Venue, l.Market, l.Status, l.ETF, l.Test} })
}

func loadTickers(ctx context.Context, q querier) ([]RefTicker, error) {
	rows, err := q.QueryContext(ctx, `SELECT ticker, name, cik, exchange FROM ref_ticker ORDER BY ticker`)
	if err != nil {
		return nil, fmt.Errorf("store: reference tickers: %w", err)
	}
	defer rows.Close()
	var out []RefTicker
	for rows.Next() {
		var t RefTicker
		if err := rows.Scan(&t.Ticker, &t.Name, &t.CIK, &t.Exchange); err != nil {
			return nil, fmt.Errorf("store: reference tickers: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func loadListings(ctx context.Context, q querier) ([]RefListing, error) {
	rows, err := q.QueryContext(ctx, `SELECT symbol, name, venue, market, status, etf, test FROM ref_listing ORDER BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("store: reference listing: %w", err)
	}
	defer rows.Close()
	var out []RefListing
	for rows.Next() {
		var l RefListing
		if err := rows.Scan(&l.Symbol, &l.Name, &l.Venue, &l.Market, &l.Status, &l.ETF, &l.Test); err != nil {
			return nil, fmt.Errorf("store: reference listing: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func loadCompanies(ctx context.Context, q querier) ([]RefCompany, error) {
	rows, err := q.QueryContext(ctx, `SELECT cik, name, sic, description, entity_type, category, state, fiscal_year,
		website, former, missing, fetched_ms, extra FROM ref_company ORDER BY cik`)
	if err != nil {
		return nil, fmt.Errorf("store: reference companies: %w", err)
	}
	defer rows.Close()
	var out []RefCompany
	for rows.Next() {
		var c RefCompany
		var former, extra string
		var fetched int64
		if err := rows.Scan(&c.CIK, &c.Name, &c.SIC, &c.Description, &c.EntityType, &c.Category, &c.State,
			&c.FiscalYear, &c.Website, &former, &c.Missing, &fetched, &extra); err != nil {
			return nil, fmt.Errorf("store: reference companies: %w", err)
		}
		var x companyExtra
		if err := json.Unmarshal([]byte(extra), &x); err != nil {
			return nil, fmt.Errorf("store: reference companies: extra data of %s: %w", c.CIK, err)
		}
		c.Exchanges, c.City, c.Region, c.Phone, c.EIN, c.LEI, c.Investor = x.Exchanges, x.City, x.Region, x.Phone, x.EIN, x.LEI, x.Investor
		if err := json.Unmarshal([]byte(former), &c.Former); err != nil {
			return nil, fmt.Errorf("store: reference companies: former names of %s: %w", c.CIK, err)
		}
		if fetched != 0 {
			c.FetchedAt = fromMS(fetched)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PutCompanies implements Reference. The fetch time is kept apart from the
// content: a record fetched again with the same content only gets a new time
// and is not a change.
func (db *DB) PutCompanies(ctx context.Context, rows []RefCompany, at time.Time) (RefDiff, error) {
	h, err := db.baseHandle()
	if err != nil {
		return RefDiff{}, err
	}
	if len(rows) == 0 {
		return RefDiff{Same: true}, nil
	}
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b RefCompany) int { return strings.Compare(a.CIK, b.CIK) })
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return RefDiff{}, fmt.Errorf("store: companies: %w", err)
	}
	defer tx.Rollback()
	old, err := loadCompanies(ctx, tx)
	if err != nil {
		return RefDiff{}, err
	}
	oldByCIK := make(map[string]RefCompany, len(old))
	for _, o := range old {
		oldByCIK[o.CIK] = o
	}
	var diff RefDiff
	var changes []RefChange
	for _, r := range rows {
		o, had := oldByCIK[r.CIK]
		if had && slices.Equal(o.fields(), r.fields()) && o.FetchedAt.Equal(r.FetchedAt) {
			continue
		}
		switch {
		case !had:
			diff.Added++
			changes = append(changes, RefChange{Kind: "added", Key: r.CIK, Detail: describe(r.fields())})
		case !slices.Equal(o.fields(), r.fields()):
			diff.Changed++
			changes = append(changes, RefChange{Kind: "changed", Key: r.CIK, Detail: describeChange(o.fields(), r.fields())})
		}
		former, _ := json.Marshal(append([]string{}, r.Former...))
		var fetched int64
		if !r.FetchedAt.IsZero() {
			fetched = ms(r.FetchedAt)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ref_company
			(cik, name, sic, description, entity_type, category, state, fiscal_year, website, former, missing, fetched_ms, extra)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (cik) DO UPDATE SET name = excluded.name, sic = excluded.sic, description = excluded.description, entity_type = excluded.entity_type, category = excluded.category, state = excluded.state, fiscal_year = excluded.fiscal_year, website = excluded.website, former = excluded.former, missing = excluded.missing, fetched_ms = excluded.fetched_ms, extra = excluded.extra`,
			r.CIK, r.Name, r.SIC, r.Description, r.EntityType, r.Category, r.State, r.FiscalYear, r.Website,
			string(former), r.Missing, fetched, r.extra()); err != nil {
			return RefDiff{}, fmt.Errorf("store: companies: %w", err)
		}
	}
	diff.Same = diff.Added == 0 && diff.Changed == 0
	if !diff.Same {
		all, err := loadCompanies(ctx, tx)
		if err != nil {
			return RefDiff{}, err
		}
		diff.Hash = hashRows(all, RefCompany.fields)
		src := RefSource{Name: SourceCompany}
		// Companies arrive in batches; the first batch is a load, not a change.
		if err := recordChange(ctx, tx, src, diff.Hash, len(all), at, len(old) > 0, changes); err != nil {
			return RefDiff{}, err
		}
	}
	return diff, tx.Commit()
}

// RefTickers implements Reference.
func (db *DB) RefTickers(ctx context.Context) ([]RefTicker, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	return loadTickers(ctx, h.r)
}

// RefListings implements Reference.
func (db *DB) RefListings(ctx context.Context) ([]RefListing, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	return loadListings(ctx, h.r)
}

// RefCompanies implements Reference.
func (db *DB) RefCompanies(ctx context.Context) ([]RefCompany, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	return loadCompanies(ctx, h.r)
}

// RefSnapshots implements Reference.
func (db *DB) RefSnapshots(ctx context.Context, source string, limit int) ([]RefSnapshot, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := h.r.QueryContext(ctx, `SELECT source, hash, records, at_ms FROM ref_snapshot
		WHERE source = ? ORDER BY id DESC LIMIT ?`, source, limit)
	if err != nil {
		return nil, fmt.Errorf("store: reference snapshots: %w", err)
	}
	defer rows.Close()
	var out []RefSnapshot
	for rows.Next() {
		var s RefSnapshot
		var at int64
		if err := rows.Scan(&s.Source, &s.Hash, &s.Records, &at); err != nil {
			return nil, fmt.Errorf("store: reference snapshots: %w", err)
		}
		s.At = fromMS(at)
		out = append(out, s)
	}
	return out, rows.Err()
}

// RefChanges implements Reference.
func (db *DB) RefChanges(ctx context.Context, since time.Time, limit int) ([]RefChange, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := h.r.QueryContext(ctx, `SELECT at_ms, source, kind, key, detail FROM ref_change
		WHERE at_ms >= ? ORDER BY id DESC LIMIT ?`, ms(since), limit)
	if err != nil {
		return nil, fmt.Errorf("store: reference changes: %w", err)
	}
	defer rows.Close()
	var out []RefChange
	for rows.Next() {
		var c RefChange
		var at int64
		if err := rows.Scan(&at, &c.Source, &c.Kind, &c.Key, &c.Detail); err != nil {
			return nil, fmt.Errorf("store: reference changes: %w", err)
		}
		c.At = fromMS(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (w RefWebsite) fields() []string { return []string{w.Ticker, w.URL, w.Label} }

func loadWebsites(ctx context.Context, q querier) ([]RefWebsite, error) {
	rows, err := q.QueryContext(ctx, `SELECT ticker, url, label, fetched_ms FROM ref_website ORDER BY ticker`)
	if err != nil {
		return nil, fmt.Errorf("store: reference websites: %w", err)
	}
	defer rows.Close()
	var out []RefWebsite
	for rows.Next() {
		var w RefWebsite
		var fetched int64
		if err := rows.Scan(&w.Ticker, &w.URL, &w.Label, &fetched); err != nil {
			return nil, fmt.Errorf("store: reference websites: %w", err)
		}
		if fetched != 0 {
			w.FetchedAt = fromMS(fetched)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RefWebsites implements Reference.
func (db *DB) RefWebsites(ctx context.Context) ([]RefWebsite, error) {
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	return loadWebsites(ctx, h.r)
}

// PutWebsites implements Reference, as PutCompanies does: the fetch time is
// kept apart from the content, so asking again and getting the same answer is
// not a change.
func (db *DB) PutWebsites(ctx context.Context, rows []RefWebsite, at time.Time) (RefDiff, error) {
	h, err := db.baseHandle()
	if err != nil {
		return RefDiff{}, err
	}
	if len(rows) == 0 {
		return RefDiff{Same: true}, nil
	}
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b RefWebsite) int { return strings.Compare(a.Ticker, b.Ticker) })
	tx, err := h.beginWrite(ctx)
	if err != nil {
		return RefDiff{}, fmt.Errorf("store: websites: %w", err)
	}
	defer tx.Rollback()
	old, err := loadWebsites(ctx, tx)
	if err != nil {
		return RefDiff{}, err
	}
	oldBy := make(map[string]RefWebsite, len(old))
	for _, o := range old {
		oldBy[o.Ticker] = o
	}
	var diff RefDiff
	var changes []RefChange
	for _, r := range rows {
		o, had := oldBy[r.Ticker]
		if had && slices.Equal(o.fields(), r.fields()) && o.FetchedAt.Equal(r.FetchedAt) {
			continue
		}
		switch {
		case !had:
			diff.Added++
			changes = append(changes, RefChange{Kind: "added", Key: r.Ticker, Detail: describe(r.fields())})
		case !slices.Equal(o.fields(), r.fields()):
			diff.Changed++
			changes = append(changes, RefChange{Kind: "changed", Key: r.Ticker, Detail: describeChange(o.fields(), r.fields())})
		}
		var fetched int64
		if !r.FetchedAt.IsZero() {
			fetched = ms(r.FetchedAt)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ref_website (ticker, url, label, fetched_ms) VALUES (?, ?, ?, ?)
		ON CONFLICT (ticker) DO UPDATE SET url = excluded.url, label = excluded.label, fetched_ms = excluded.fetched_ms`,
			r.Ticker, r.URL, r.Label, fetched); err != nil {
			return RefDiff{}, fmt.Errorf("store: websites: %w", err)
		}
	}
	diff.Same = diff.Added == 0 && diff.Changed == 0
	if !diff.Same {
		all, err := loadWebsites(ctx, tx)
		if err != nil {
			return RefDiff{}, err
		}
		diff.Hash = hashRows(all, RefWebsite.fields)
		if err := recordChange(ctx, tx, RefSource{Name: SourceWebsite}, diff.Hash, len(all), at, len(old) > 0, changes); err != nil {
			return RefDiff{}, err
		}
	}
	return diff, tx.Commit()
}
