package edgar

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// IndexEntry is one filing of the daily index: what the SEC lists as filed on a
// day, whatever the live feed delivered.
type IndexEntry struct {
	CIK       string // ten digits
	Company   string
	Form      string
	Filed     string // YYYY-MM-DD
	Accession Accession
}

// DailyIndexURL is the master index of the filings of one day.
func (c Config) DailyIndexURL(day time.Time) string {
	c = c.withDefaults()
	q := (int(day.Month())-1)/3 + 1
	return fmt.Sprintf("%s/Archives/edgar/daily-index/%d/QTR%d/master.%s.idx", c.WebBase, day.Year(), q, day.Format("20060102"))
}

// ParseDailyIndex reads a master index: a header, a line of dashes, then lines
// "CIK|Company Name|Form Type|Date Filed|Filename". A line that does not have
// those five fields, or whose file name holds no accession number, is counted as
// bad; an index with entries and more than a tenth bad lines is an error, the
// format has changed.
func ParseDailyIndex(body []byte) (entries []IndexEntry, bad int, err error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	started := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if !started {
			started = strings.HasPrefix(line, "----")
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 5 {
			bad++
			continue
		}
		file := f[4]
		base := file[strings.LastIndex(file, "/")+1:]
		acc, perr := ParseAccession(strings.TrimSuffix(base, ".txt"))
		filed := strings.TrimSpace(f[3])
		if t, terr := time.Parse("20060102", filed); terr == nil {
			filed = t.Format(time.DateOnly)
		} else if _, terr := time.Parse(time.DateOnly, filed); terr != nil {
			perr = errors.Join(perr, terr)
		}
		cik := padCIK(strings.TrimSpace(f[0]))
		if perr != nil || cik == "" {
			bad++
			continue
		}
		entries = append(entries, IndexEntry{CIK: cik, Company: strings.TrimSpace(f[1]), Form: strings.TrimSpace(f[2]), Filed: filed, Accession: acc})
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("edgar: read the daily index: %w", err)
	}
	if !started {
		return nil, 0, errors.New("edgar: the daily index has no table (the format has changed)")
	}
	if bad > 0 && bad*10 > len(entries)+bad {
		return nil, bad, fmt.Errorf("edgar: %d of %d lines of the daily index are unreadable (the format has changed)", bad, len(entries)+bad)
	}
	return entries, bad, nil
}

// Indexer reads the daily indexes.
type Indexer struct {
	Client httpx.Client
	Config Config
}

// Day reads the index of one day. found is false when the SEC has no index for
// the day (a weekend, a holiday, or a day not published yet); that is not an
// error and it is not the same as an empty day.
func (ix Indexer) Day(ctx context.Context, day time.Time) (entries []IndexEntry, found bool, err error) {
	resp, err := ix.Client.Do(ctx, httpx.Request{URL: ix.Config.DailyIndexURL(day)})
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == 404 {
			return nil, false, nil
		}
		return nil, false, err
	}
	entries, _, err = ParseDailyIndex(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return entries, true, nil
}

// Wanted keeps the entries of the forms (an amendment counts as its form).
func Wanted(entries []IndexEntry, forms []string) []IndexEntry {
	var out []IndexEntry
	for _, e := range entries {
		if keepForm(e.Form, forms) {
			out = append(out, e)
		}
	}
	return out
}

// Article makes the article for a filing the live feed missed. It carries what
// the index knows: the filing day (not the time), so Published is the start of
// that day and the score reasons say so.
func (c Config) Article(src model.Source, e IndexEntry, now time.Time) model.Article {
	day, _ := time.Parse(time.DateOnly, e.Filed)
	a := model.Article{
		ID:        string(e.Accession),
		SourceID:  src.ID,
		Domain:    domain,
		URL:       FilingIndexURL(e.CIK, e.Accession),
		Title:     e.Form + " - " + e.Company,
		Summary:   fmt.Sprintf("Form %s filed by %s (CIK %s), accession %s", e.Form, e.Company, e.CIK, e.Accession),
		Published: day,
		Fetched:   now,
		Form:      e.Form,
		CIK:       e.CIK,
		Tickers:   slices.Clone(c.Tickers[e.CIK]),
	}
	a.Score.Reasons = []string{"filing time unknown: found in the daily index, dated to the start of its filing day"}
	return a
}
