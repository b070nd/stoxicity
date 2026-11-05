package company

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// The Nasdaq Trader symbol directory lists every security on Nasdaq and on the
// other US exchanges, with the one fact the SEC list lacks: whether it is an
// ETF. The files are pipe-separated text with a header line and a footer line
// ("File Creation Time: ..."). Nasdaq's copyright page allows one personal,
// non-commercial copy; this program keeps that copy in the data folder and
// never passes it on.
const (
	// NasdaqListedURL is the file of Nasdaq-listed securities.
	NasdaqListedURL = "https://www.nasdaqtrader.com/dynamic/SymDir/nasdaqlisted.txt"
	// OtherListedURL is the file of securities listed on the other exchanges.
	OtherListedURL = "https://www.nasdaqtrader.com/dynamic/SymDir/otherlisted.txt"

	nasdaqFile = "nasdaqlisted.txt"
	otherFile  = "otherlisted.txt"
)

// Security is one line of the symbol directory.
type Security struct {
	Symbol string // normalised, see NormalizeSymbol
	Name   string
	// Venue is the exchange that lists it: "Nasdaq", "NYSE", "NYSE American",
	// "NYSE Arca", "Cboe BZX", "IEX", or the raw code when it is not known.
	Venue string
	// Market is the Nasdaq tier ("Global Select", "Global", "Capital"); empty
	// for the other exchanges.
	Market string
	// Status is "" when the issuer is in good standing, else what the directory
	// reports: "deficient", "delinquent", "bankrupt" or a combination.
	Status string
	ETF    bool
	Test   bool
}

// Listing is the symbol directory, by normalised symbol.
type Listing struct {
	bySymbol map[string]Security
	// FetchedAt is the older of the times the two files were confirmed
	// current; zero when there is no cache.
	FetchedAt time.Time
}

// Len is the number of securities.
func (l Listing) Len() int { return len(l.bySymbol) }

// Get returns the security with the ticker, written either way (BRK-B, BRK.B).
func (l Listing) Get(ticker string) (Security, bool) {
	s, ok := l.bySymbol[NormalizeSymbol(ticker)]
	return s, ok
}

// NormalizeSymbol writes a symbol the way the SEC list does: upper case, with
// the separators the exchanges use for a share class or a suffix ("." "/" "$"
// and spaces) turned into a hyphen.
func NormalizeSymbol(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		switch r {
		case '.', '/', '$', ' ':
			return '-'
		}
		return r
	}, s)
}

var nasdaqMarkets = map[string]string{"Q": "Global Select", "G": "Global", "S": "Capital"}

var otherVenues = map[string]string{
	"A": "NYSE American", "N": "NYSE", "P": "NYSE Arca", "Z": "Cboe BZX", "V": "IEX",
}

// statusText turns the directory's Financial Status letter into words.
func statusText(code string) string {
	switch code {
	case "D":
		return "deficient"
	case "E":
		return "delinquent"
	case "Q":
		return "bankrupt"
	case "G":
		return "deficient, bankrupt"
	case "H":
		return "deficient, delinquent"
	case "J":
		return "delinquent, bankrupt"
	case "K":
		return "deficient, delinquent, bankrupt"
	}
	return "" // N is normal; an unknown letter says nothing
}

// parseDirectory reads one file. It finds columns by the names in the header
// line, so a reordered file still reads, and returns an error when the header
// lacks the first column or there is no data row.
func parseDirectory(raw []byte, nasdaq bool) ([]Security, error) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var cols map[string]int
	var out []Security
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "File Creation Time") {
			continue
		}
		f := strings.Split(line, "|")
		if cols == nil {
			cols = map[string]int{}
			for i, h := range f {
				cols[strings.ToLower(strings.TrimSpace(h))] = i
			}
			if _, ok := cols[map[bool]string{true: "symbol", false: "act symbol"}[nasdaq]]; !ok {
				return nil, errors.New("company: the symbol directory has an unexpected header")
			}
			continue
		}
		get := func(name string) string {
			if i, ok := cols[name]; ok && i < len(f) {
				return strings.TrimSpace(f[i])
			}
			return ""
		}
		var s Security
		if nasdaq {
			s = Security{
				Symbol: NormalizeSymbol(get("symbol")), Name: get("security name"), Venue: "Nasdaq",
				Market: nasdaqMarkets[get("market category")], Status: statusText(get("financial status")),
				ETF: get("etf") == "Y", Test: get("test issue") == "Y",
			}
		} else {
			code := get("exchange")
			venue := otherVenues[code]
			if venue == "" {
				venue = code
			}
			s = Security{
				Symbol: NormalizeSymbol(get("act symbol")), Name: get("security name"), Venue: venue,
				ETF: get("etf") == "Y", Test: get("test issue") == "Y",
			}
		}
		if s.Symbol != "" {
			out = append(out, s)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("company: read the symbol directory: %w", err)
	}
	if cols == nil || len(out) == 0 {
		return nil, errors.New("company: the symbol directory has no securities")
	}
	return out, nil
}

// LoadListing reads the cached files in dir. A missing cache gives an empty
// Listing and no error; a damaged file is an error.
func LoadListing(dir string) (Listing, error) {
	l := Listing{bySymbol: map[string]Security{}}
	var oldest time.Time
	any := false
	for _, f := range []struct {
		file   string
		nasdaq bool
	}{{nasdaqFile, true}, {otherFile, false}} {
		raw, err := os.ReadFile(filepath.Join(dir, f.file))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Listing{}, fmt.Errorf("company: %w", err)
		}
		secs, err := parseDirectory(raw, f.nasdaq)
		if err != nil {
			return Listing{}, fmt.Errorf("company: cached %s is damaged (delete it and run \"stoxicity universe update\"): %w", f.file, err)
		}
		for _, s := range secs {
			if _, dup := l.bySymbol[s.Symbol]; !dup {
				l.bySymbol[s.Symbol] = s
			}
		}
		any = true
		if md, ok := readMetaFile(dir, metaName(f.file)); ok && (oldest.IsZero() || md.FetchedAt.Before(oldest)) {
			oldest = md.FetchedAt
		}
	}
	if any {
		l.FetchedAt = oldest
	}
	return l, nil
}

func metaName(file string) string { return strings.TrimSuffix(file, filepath.Ext(file)) + ".meta.json" }

func readMetaFile(dir, name string) (meta, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return meta{}, false
	}
	var md meta
	if json.Unmarshal(raw, &md) != nil {
		return meta{}, false
	}
	return md, true
}

// ListingOptions are the settings of RefreshListing.
type ListingOptions struct {
	NasdaqURL, OtherURL string // replace the defaults; tests point them at a server
	MaxAge              time.Duration
	Force               bool
	Now                 func() time.Time
}

// RefreshListing brings the two cached files in dir up to date, each as
// Refresh does the ticker list: nothing is asked while the cache is young, a
// 304 only renews the time, and an answer that does not parse or holds under
// half the cached securities is refused.
func RefreshListing(ctx context.Context, client httpx.Client, dir string, opts ListingOptions) (Result, error) {
	if opts.NasdaqURL == "" {
		opts.NasdaqURL = NasdaqListedURL
	}
	if opts.OtherURL == "" {
		opts.OtherURL = OtherListedURL
	}
	var res Result
	res.Skipped = true
	for _, f := range []struct {
		file, url string
		nasdaq    bool
	}{{nasdaqFile, opts.NasdaqURL, true}, {otherFile, opts.OtherURL, false}} {
		n, updated, skipped, err := refreshDirectory(ctx, client, dir, f.file, f.url, f.nasdaq, opts)
		if err != nil {
			return res, err
		}
		res.Records += n
		res.Updated = res.Updated || updated
		res.Skipped = res.Skipped && skipped
	}
	return res, nil
}

func refreshDirectory(ctx context.Context, client httpx.Client, dir, file, url string, nasdaq bool, opts ListingOptions) (records int, updated, skipped bool, err error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	var oldN int
	if raw, rerr := os.ReadFile(filepath.Join(dir, file)); rerr == nil {
		if secs, perr := parseDirectory(raw, nasdaq); perr == nil {
			oldN = len(secs)
		}
	}
	prev, havePrev := readMetaFile(dir, metaName(file))
	if !opts.Force && oldN > 0 && havePrev && now().Sub(prev.FetchedAt) < maxAge {
		return oldN, false, true, nil
	}
	req := httpx.Request{URL: url}
	if havePrev && oldN > 0 {
		req.ETag, req.LastModified = prev.ETag, prev.LastModified
	}
	resp, err := client.Do(ctx, req)
	if err != nil {
		return 0, false, false, fmt.Errorf("company: ask for %s: %w", file, err)
	}
	if resp.NotModified {
		if oldN == 0 {
			return 0, false, false, fmt.Errorf("company: the server says %s is unchanged but there is no cached copy", file)
		}
		prev.FetchedAt = now().UTC()
		return oldN, false, false, writeMetaFile(dir, metaName(file), prev)
	}
	secs, err := parseDirectory(resp.Body, nasdaq)
	if err != nil {
		return 0, false, false, fmt.Errorf("company: %s: %w", file, err)
	}
	if oldN > 0 && float64(len(secs)) < minKeep*float64(oldN) {
		return 0, false, false, fmt.Errorf("company: the new %s has %d securities against %d cached; keeping the cached file", file, len(secs), oldN)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, false, false, fmt.Errorf("company: %w", err)
	}
	if err := writeFile(filepath.Join(dir, file), resp.Body); err != nil {
		return 0, false, false, err
	}
	err = writeMetaFile(dir, metaName(file), meta{
		ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt: now().UTC(), Records: len(secs),
	})
	return len(secs), err == nil, false, err
}

func writeMetaFile(dir, name string, md meta) error {
	raw, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return fmt.Errorf("company: %w", err)
	}
	return writeFile(filepath.Join(dir, name), append(raw, '\n'))
}
