// Package company keeps the company master: the SEC's list of US-listed
// tickers with their names and CIKs, cached on disk. It feeds two readers: the
// entity index, which finds companies in text, and the EDGAR fetchers, which
// need the CIK to ticker map to put tickers on filings.
//
// The list is https://www.sec.gov/files/company_tickers_exchange.json, which
// adds the exchange to each record (company_tickers.json, without it, is read
// too). The SEC says it updates the files "periodically" and does not guarantee
// their accuracy or scope, and the names are EDGAR conformed names (often upper
// case, with suffixes such as CORP). Every use of the list is therefore a
// convenience that a person can override with the watchlist, not a source of
// truth.
//
// Two more files add to the list. listing.go reads the Nasdaq Trader symbol
// directory, which says whether a symbol is an ETF and gives the market tier
// and financial status. industry.go reads the SEC's per-company submissions,
// which give the industry (SIC code), state and fiscal year end for the
// companies you follow.
package company

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// DefaultURL is the SEC's ticker list with exchanges.
const DefaultURL = "https://www.sec.gov/files/company_tickers_exchange.json"

// DefaultMaxAge is how old the cached list may be before Refresh asks the SEC
// again. The SEC gives no schedule; a week keeps the load negligible.
const DefaultMaxAge = 7 * 24 * time.Hour

const (
	listFile = "company_tickers.json"
	metaFile = "company_tickers.meta.json"
	// minKeep is the share of the cached list that a new one must have, below
	// which it is taken as damaged and refused.
	minKeep = 0.5
)

// Master is the cached list.
type Master struct {
	// Entities holds one entry per ticker, sorted by ticker.
	Entities []model.Entity
	// FetchedAt is when the list was last confirmed current; zero if there is
	// no cache.
	FetchedAt time.Time
}

// Empty reports whether there is no list.
func (m Master) Empty() bool { return len(m.Entities) == 0 }

// Age is how long ago the list was last confirmed current at now; zero when
// there is no cache.
func (m Master) Age(now time.Time) time.Duration {
	if m.FetchedAt.IsZero() {
		return 0
	}
	return now.Sub(m.FetchedAt)
}

// meta is stored beside the list.
type meta struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
	Records      int       `json:"records"`
}

// Load reads the cache in dir. A missing cache is not an error: the result is
// empty. A damaged one is an error.
func Load(dir string) (Master, error) {
	raw, err := os.ReadFile(filepath.Join(dir, listFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Master{}, nil
	}
	if err != nil {
		return Master{}, fmt.Errorf("company: %w", err)
	}
	ents, err := edgar.ParseTickers(bytes.NewReader(raw))
	if err != nil {
		return Master{}, fmt.Errorf("company: cached list in %s is damaged (delete it and run \"stoxicity company update\"): %w", dir, err)
	}
	m := Master{Entities: ents}
	if md, ok := readMeta(dir); ok {
		m.FetchedAt = md.FetchedAt
	}
	return m, nil
}

func readMeta(dir string) (meta, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return meta{}, false
	}
	var md meta
	if json.Unmarshal(raw, &md) != nil {
		return meta{}, false
	}
	return md, true
}

// Result says what Refresh did.
type Result struct {
	// Updated is true when a new list was stored.
	Updated bool
	// Skipped is true when the cache was young enough and nothing was asked.
	Skipped bool
	// Records is the number of tickers in the list now stored.
	Records int
}

// Options are the settings of Refresh.
type Options struct {
	// URL replaces DefaultURL; tests point it at a server.
	URL string
	// MaxAge replaces DefaultMaxAge.
	MaxAge time.Duration
	// Force asks even when the cache is young.
	Force bool
	// Now is the clock; time.Now if nil.
	Now func() time.Time
}

// Refresh brings the cache in dir up to date. When the cache is younger than
// MaxAge it asks nothing. Otherwise it sends a conditional request with the
// stored ETag and Last-Modified: a 304 only renews the fetch time. A new list
// is parsed first and replaces the old one only if it is valid and holds at
// least half as many tickers, so a damaged answer cannot wipe a good cache.
// Files are written by rename, so a reader never sees half a file.
func Refresh(ctx context.Context, client httpx.Client, dir string, opts Options) (Result, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	url := opts.URL
	if url == "" {
		url = DefaultURL
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}

	old, err := Load(dir)
	if err != nil {
		// A damaged cache is replaced, not repaired.
		old = Master{}
	}
	if !opts.Force && !old.Empty() && !old.FetchedAt.IsZero() && now().Sub(old.FetchedAt) < maxAge {
		return Result{Skipped: true, Records: len(old.Entities)}, nil
	}

	req := httpx.Request{URL: url}
	prev, havePrev := readMeta(dir)
	if havePrev && !old.Empty() {
		req.ETag, req.LastModified = prev.ETag, prev.LastModified
	}
	resp, err := client.Do(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("company: ask for the ticker list: %w", err)
	}
	if resp.NotModified {
		if old.Empty() {
			return Result{}, errors.New("company: the server says the list is unchanged but there is no cached list")
		}
		prev.FetchedAt = now().UTC()
		if err := writeMeta(dir, prev); err != nil {
			return Result{}, err
		}
		return Result{Records: len(old.Entities)}, nil
	}
	ents, err := edgar.ParseTickers(bytes.NewReader(resp.Body))
	if err != nil {
		return Result{}, fmt.Errorf("company: the answer is not the expected ticker list: %w", err)
	}
	if len(ents) == 0 {
		return Result{}, errors.New("company: the ticker list in the answer is empty")
	}
	if n := len(old.Entities); n > 0 && float64(len(ents)) < minKeep*float64(n) {
		return Result{}, fmt.Errorf("company: the new list has %d tickers against %d cached; keeping the cached list", len(ents), n)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("company: %w", err)
	}
	if err := writeFile(filepath.Join(dir, listFile), resp.Body); err != nil {
		return Result{}, err
	}
	err = writeMeta(dir, meta{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    now().UTC(),
		Records:      len(ents),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Updated: true, Records: len(ents)}, nil
}

func writeMeta(dir string, md meta) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("company: %w", err)
	}
	raw, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return fmt.Errorf("company: %w", err)
	}
	return writeFile(filepath.Join(dir, metaFile), append(raw, '\n'))
}

// writeFile writes data to path through a temporary file and a rename.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("company: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(name)
		return fmt.Errorf("company: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("company: %w", err)
	}
	return nil
}

// Find returns up to limit entities whose ticker or name contains query,
// ignoring case; an exact ticker comes first, then tickers that start with it,
// then names.
func (m Master) Find(query string, limit int) []model.Entity {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || limit <= 0 {
		return nil
	}
	rank := func(e model.Entity) int {
		t, n := strings.ToLower(e.Ticker), strings.ToLower(e.Name)
		switch {
		case t == q:
			return 0
		case strings.HasPrefix(t, q):
			return 1
		case strings.HasPrefix(n, q):
			return 2
		case strings.Contains(n, q):
			return 3
		}
		return -1
	}
	type hit struct {
		e model.Entity
		r int
	}
	var hits []hit
	for _, e := range m.Entities {
		if r := rank(e); r >= 0 {
			hits = append(hits, hit{e, r})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.r - b.r })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]model.Entity, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}
