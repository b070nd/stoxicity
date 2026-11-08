package quotes

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/money"
)

// The ECB euro foreign exchange reference rates: a free, public, keyless feed of
// units of foreign currency per 1 EUR, set once on each TARGET working day. They
// are for information and reporting; they are not rates anything can be traded at.
const (
	ECBDailyURL   = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
	ECB90DayURL   = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist-90d.xml"
	ECBHistoryURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.xml"
	// ECBSource is the Source written on every observation from this feed.
	ECBSource = "ecb"
)

// ECBReport is what a parse found.
type ECBReport struct {
	Days    int
	Skipped map[string]int // currency code -> days skipped: not a currency this system handles
}

type ecbEnvelope struct {
	Days []struct {
		Time  string `xml:"time,attr"`
		Rates []struct {
			Currency string `xml:"currency,attr"`
			Rate     string `xml:"rate,attr"`
		} `xml:"Cube"`
	} `xml:"Cube>Cube"`
}

// ParseECB reads an ECB reference-rate document (daily, 90-day or full history;
// they share one shape). Each rate becomes EUR/<currency> with the reference
// basis. A currency this system has no minor-unit table for is skipped and
// counted, not guessed; a malformed rate or date fails the whole document, so a
// half-read file never looks complete.
func ParseECB(r io.Reader, ref string, received time.Time) ([]FXObs, ECBReport, error) {
	rep := ECBReport{Skipped: map[string]int{}}
	var env ecbEnvelope
	dec := xml.NewDecoder(r)
	dec.Strict = true
	if err := dec.Decode(&env); err != nil {
		return nil, rep, fmt.Errorf("quotes: read ECB rates: %w", err)
	}
	if len(env.Days) == 0 {
		return nil, rep, errors.New("quotes: the ECB document holds no dated rates")
	}
	var out []FXObs
	for _, d := range env.Days {
		if !validDay(d.Time) {
			return nil, rep, fmt.Errorf("quotes: ECB date %q is not YYYY-MM-DD", d.Time)
		}
		rep.Days++
		for _, c := range d.Rates {
			cur, err := money.ParseCurrency(c.Currency)
			if err != nil {
				rep.Skipped[c.Currency]++
				continue
			}
			v, err := money.ParseDecimal(c.Rate)
			if err != nil {
				return nil, rep, fmt.Errorf("quotes: ECB rate for %s on %s: %w", c.Currency, d.Time, err)
			}
			o := FXObs{Base: "EUR", Quote: cur, Value: v, Basis: money.BasisReference, Day: d.Time, Source: ECBSource, SourceRef: ref, Received: received}
			if err := o.Validate(); err != nil {
				return nil, rep, err
			}
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Quote < out[j].Quote
	})
	return out, rep, nil
}

// ECB fetches the reference rates. It makes one GET per call and nothing else.
type ECB struct {
	Client httpx.Client
	// URL replaces the address; tests point it at a server.
	URL string
	// Now is the time stamped on what is received.
	Now func() time.Time
}

// Fetch reads the document at the address (the daily one when URL is empty).
func (e ECB) Fetch(ctx context.Context, url string) ([]FXObs, ECBReport, error) {
	if url == "" {
		url = e.URL
	}
	if url == "" {
		url = ECBDailyURL
	}
	resp, err := e.Client.Do(ctx, httpx.Request{URL: url})
	if err != nil {
		return nil, ECBReport{}, fmt.Errorf("quotes: fetch ECB rates: %w", err)
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	return ParseECB(bytes.NewReader(resp.Body), url, now())
}
