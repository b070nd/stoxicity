package edgar

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
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
	return c.ArticleAt(src, e, now, time.Time{})
}

// ArticleAt is Article with the acceptance time of the filing when it is known
// (a zero time means it is not). A known time becomes the publication time; an
// unknown one is never made up: the article is dated to the start of its filing
// day and says so.
func (c Config) ArticleAt(src model.Source, e IndexEntry, now, accepted time.Time) model.Article {
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
	if !accepted.IsZero() {
		a.Published = accepted.UTC()
		a.Score.Reasons = []string{"recovered after the live feed missed it: acceptance time from the SEC's record of the company"}
	} else {
		a.Score.Reasons = []string{"filing time unknown: found in the daily index, dated to the start of its filing day"}
	}
	return a
}

// QuarterlyIndexURL is the compressed master index of the filings of one
// quarter. It lists every filing of the quarter in the same format as a daily
// index; a quarter that has ended is complete.
func (c Config) QuarterlyIndexURL(year, quarter int) string {
	c = c.withDefaults()
	return fmt.Sprintf("%s/Archives/edgar/full-index/%d/QTR%d/master.gz", c.WebBase, year, quarter)
}

// Quarter reads the index of one quarter. found is false when the SEC has none
// (the quarter has not ended, or is not published yet). The answer is gzip data;
// the HTTP client's size limit applies to it as sent.
func (ix Indexer) Quarter(ctx context.Context, year, quarter int) (entries []IndexEntry, found bool, err error) {
	resp, err := ix.Client.Do(ctx, httpx.Request{URL: ix.Config.QuarterlyIndexURL(year, quarter)})
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == 404 {
			return nil, false, nil
		}
		return nil, false, err
	}
	body := resp.Body
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, true, fmt.Errorf("edgar: read the quarterly index: %w", err)
		}
		defer zr.Close()
		if body, err = io.ReadAll(io.LimitReader(zr, 1<<30)); err != nil {
			return nil, true, fmt.Errorf("edgar: read the quarterly index: %w", err)
		}
	}
	entries, _, err = ParseDailyIndex(body)
	if err != nil {
		return nil, true, err
	}
	return entries, true, nil
}

// QuarterOf is the quarter (1 to 4) a day is in.
func QuarterOf(day time.Time) int { return (int(day.Month())-1)/3 + 1 }

// NonFilingDay says whether the SEC takes no filings on the day by the
// calendar, and why: a weekend (certain) or a federal holiday (the usual rule;
// a special closure is not known and is found only by the index being absent).
func NonFilingDay(day time.Time) (reason string, weekend bool, nonFiling bool) {
	switch day.Weekday() {
	case time.Saturday:
		return "Saturday", true, true
	case time.Sunday:
		return "Sunday", true, true
	}
	if name, ok := federalHoliday(day); ok {
		return name, false, true
	}
	return "", false, false
}

// federalHoliday names the U.S. federal holiday observed on the day, if any: a
// holiday on a Saturday is observed on the Friday before, one on a Sunday on the
// Monday after.
func federalHoliday(d time.Time) (string, bool) {
	check := func(y int) (string, bool) {
		nth := func(month time.Month, weekday time.Weekday, n int) time.Time {
			first := time.Date(y, month, 1, 0, 0, 0, 0, time.UTC)
			off := (int(weekday) - int(first.Weekday()) + 7) % 7
			return first.AddDate(0, 0, off+7*(n-1))
		}
		last := func(month time.Month, weekday time.Weekday) time.Time {
			end := time.Date(y, month+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
			off := (int(end.Weekday()) - int(weekday) + 7) % 7
			return end.AddDate(0, 0, -off)
		}
		fixed := func(month time.Month, day int) time.Time {
			t := time.Date(y, month, day, 0, 0, 0, 0, time.UTC)
			switch t.Weekday() {
			case time.Saturday:
				return t.AddDate(0, 0, -1)
			case time.Sunday:
				return t.AddDate(0, 0, 1)
			}
			return t
		}
		list := []struct {
			name string
			day  time.Time
		}{
			{"New Year's Day", fixed(time.January, 1)},
			{"Martin Luther King Jr. Day", nth(time.January, time.Monday, 3)},
			{"Presidents' Day", nth(time.February, time.Monday, 3)},
			{"Memorial Day", last(time.May, time.Monday)},
			{"Juneteenth", fixed(time.June, 19)},
			{"Independence Day", fixed(time.July, 4)},
			{"Labor Day", nth(time.September, time.Monday, 1)},
			{"Columbus Day", nth(time.October, time.Monday, 2)},
			{"Veterans Day", fixed(time.November, 11)},
			{"Thanksgiving", nth(time.November, time.Thursday, 4)},
			{"Christmas Day", fixed(time.December, 25)},
		}
		for _, h := range list {
			if h.day.Equal(d) {
				return h.name, true
			}
		}
		return "", false
	}
	// A holiday of next year's January 1st can be observed on December 31st.
	if n, ok := check(d.Year()); ok {
		return n, true
	}
	if d.Month() == time.December && d.Day() == 31 {
		if n, ok := check(d.Year() + 1); ok {
			return n, true
		}
	}
	return "", false
}
