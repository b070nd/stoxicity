// Package fundamentals keeps what the SEC's XBRL frames say about every filer:
// revenue, net income, assets, equity and the shares outstanding. One request
// to the frames API answers for all companies at once, so a few dozen small
// requests a week cover the market; nothing here needs a key.
//
// A frame holds, for each company, the one fact that fits a calendar period
// best, together with the real dates of that fact. Companies with a fiscal
// year that is not the calendar year (Apple ends in September, Microsoft in
// June) therefore appear with their own year-end, and the periods are read
// from those dates, never assumed from the name of the frame.
package fundamentals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// URL is the SEC's frames API; it ends with a slash.
const URL = "https://data.sec.gov/api/xbrl/frames/"

// The metrics a Fact can be of.
const (
	Revenue   = "revenue"    // a year
	NetIncome = "income"     // a year
	Assets    = "assets"     // at a date
	Equity    = "equity"     // at a date
	Shares    = "shares"     // on the cover page, at a date
	AvgShares = "shares_avg" // weighted average over a year, the fallback for Shares
)

// The metrics that are read from a company's history only (History), never from the frames.
const (
	OperatingIncome   = "operating_income"   // a year
	OperatingCashFlow = "operating_cashflow" // a year
)

// Fact is one number of one company, for the period that ended on End. Start is
// empty for a value at a date.
type Fact struct {
	CIK        string // ten digits
	Metric     string
	Start, End string // YYYY-MM-DD
	Value      float64

	// Where the figure comes from. Accession is the filing (with dashes); Filed
	// is the day it was filed, empty when the source does not say (the frames).
	// Dimensions is empty for the consolidated figure.
	Accession, Taxonomy, Concept, Unit, Form, Filed, Dimensions string
}

// source is a concept in a taxonomy, in the order of preference for its metric. A historyOnly
// source is read from a company's facts (History) and is not asked of the frames API, which would
// add a request per period to the weekly job for every such concept.
type source struct {
	metric, taxonomy, concept, unit string
	instant, historyOnly            bool
}

var sources = []source{
	{Revenue, "us-gaap", "Revenues", "USD", false, false},
	{Revenue, "us-gaap", "RevenueFromContractWithCustomerExcludingAssessedTax", "USD", false, false},
	{Revenue, "us-gaap", "RevenueFromContractWithCustomerIncludingAssessedTax", "USD", false, false},
	{Revenue, "us-gaap", "SalesRevenueNet", "USD", false, false},
	{NetIncome, "us-gaap", "NetIncomeLoss", "USD", false, false},
	{NetIncome, "us-gaap", "ProfitLoss", "USD", false, false},
	{AvgShares, "us-gaap", "WeightedAverageNumberOfSharesOutstandingBasic", "shares", false, false},
	{Assets, "us-gaap", "Assets", "USD", true, false},
	{Equity, "us-gaap", "StockholdersEquity", "USD", true, false},
	{Equity, "us-gaap", "StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest", "USD", true, false},
	{Shares, "dei", "EntityCommonStockSharesOutstanding", "shares", true, false},
	{OperatingIncome, "us-gaap", "OperatingIncomeLoss", "USD", false, true},
	{OperatingCashFlow, "us-gaap", "NetCashProvidedByUsedInOperatingActivities", "USD", false, true},
}

// Fetcher asks the frames API.
type Fetcher struct {
	Client httpx.Client
	// URL replaces the SEC's address (it ends with a slash); tests point it at a server.
	URL string
	// HistoryURL replaces CompanyFactsURL.
	HistoryURL string
	Now        func() time.Time
}

func (f Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now().UTC()
	}
	return time.Now().UTC()
}

// annualYears are the calendar years whose annual frames are asked: the year
// that is running and the two before it, which holds the latest fiscal year of
// every company (a fiscal year ending in March 2026 is in the frame of 2025 or
// of 2026) and the one before it, for growth.
func annualYears(now time.Time) []int {
	y := now.Year()
	return []int{y - 2, y - 1, y}
}

// quarterEnds are the quarters whose date frames are asked: the last six that
// have ended, which hold the newest balance sheet and cover page of every
// company that files quarterly.
func quarterEnds(now time.Time) []string {
	q := (int(now.Month()) - 1) / 3 // quarters ended so far this year
	y := now.Year()
	var out []string
	for i := 0; i < 6; i++ {
		if q == 0 {
			q, y = 4, y-1
		}
		out = append(out, fmt.Sprintf("CY%dQ%dI", y, q))
		q--
	}
	return out
}

// Report says what a fetch did.
type Report struct {
	Requests, Missing int // frames asked; frames the SEC does not have (yet)
	Facts, Companies  int
}

// Fetch asks every frame and returns the facts. A frame the SEC does not have
// (a period too new) is skipped; any other failure ends the fetch, because a
// half-read market would rank companies on different footing.
func (f Fetcher) Fetch(ctx context.Context) ([]Fact, Report, error) {
	base := f.URL
	if base == "" {
		base = URL
	}
	var rep Report
	type key struct{ cik, metric, end string }
	seen := map[key]bool{}
	var facts []Fact
	now := f.now()
	for _, s := range sources {
		if s.historyOnly {
			continue
		}
		var periods []string
		if s.instant {
			periods = quarterEnds(now)
		} else {
			for _, y := range annualYears(now) {
				periods = append(periods, fmt.Sprintf("CY%d", y))
			}
		}
		for _, p := range periods {
			if err := ctx.Err(); err != nil {
				return nil, rep, err
			}
			rep.Requests++
			resp, err := f.Client.Do(ctx, httpx.Request{URL: fmt.Sprintf("%s%s/%s/%s/%s.json", base, s.taxonomy, s.concept, s.unit, p)})
			if err != nil {
				var he *httpx.Error
				if errors.As(err, &he) && he.Status == 404 {
					rep.Missing++
					continue
				}
				return nil, rep, fmt.Errorf("fundamentals: %s %s: %w", s.concept, p, err)
			}
			got, err := parseFrame(resp.Body, s)
			if err != nil {
				return nil, rep, fmt.Errorf("fundamentals: %s %s: %w", s.concept, p, err)
			}
			for _, x := range got {
				// The concepts are in order of preference: a later one only fills
				// the periods an earlier one did not report.
				k := key{x.CIK, x.Metric, x.End}
				if !seen[k] {
					seen[k] = true
					facts = append(facts, x)
				}
			}
		}
	}
	companies := map[string]bool{}
	for _, x := range facts {
		companies[x.CIK] = true
	}
	rep.Facts, rep.Companies = len(facts), len(companies)
	return facts, rep, nil
}

// parseFrame reads one frame. A fact of a year whose dates do not span about a
// year (a transition period, a quarter filed as a year) is left out.
func parseFrame(body []byte, s source) ([]Fact, error) {
	var ans struct {
		Data []struct {
			CIK   int64   `json:"cik"`
			Start string  `json:"start"`
			End   string  `json:"end"`
			Val   float64 `json:"val"`
			Accn  string  `json:"accn"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, fmt.Errorf("read the answer: %w", err)
	}
	out := make([]Fact, 0, len(ans.Data))
	for _, d := range ans.Data {
		if d.CIK <= 0 || d.End == "" || math.IsNaN(d.Val) || math.IsInf(d.Val, 0) {
			continue
		}
		x := Fact{CIK: fmt.Sprintf("%010d", d.CIK), Metric: s.metric, End: d.End, Value: d.Val,
			Accession: d.Accn, Taxonomy: s.taxonomy, Concept: s.concept, Unit: s.unit}
		if !s.instant {
			days, ok := daysBetween(d.Start, d.End)
			if !ok || days < 350 || days > 380 {
				continue
			}
			x.Start = d.Start
		}
		out = append(out, x)
	}
	return out, nil
}

func daysBetween(from, to string) (int, bool) {
	a, err1 := time.Parse(time.DateOnly, from)
	b, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return int(b.Sub(a).Hours() / 24), true
}

// Summary is what is known of one company's size and profit, from its latest
// fiscal year. Num holds the values by name: revenue, income, assets, equity,
// shares, growth, margin, roe and equity_ratio; a name is missing when the
// filings do not give what it needs. End is the end of the fiscal year the
// income figures are for.
type Summary struct {
	End string
	Num map[string]float64
}

// MarketCap is the shares outstanding times the price; false when either is
// unknown.
func (s Summary) MarketCap(price float64) (float64, bool) {
	sh, ok := s.Num[Shares]
	if !ok || price <= 0 {
		return 0, false
	}
	return sh * price, true
}

// Summarize reads the facts of every company into its Summary.
func Summarize(facts []Fact) map[string]Summary {
	by := map[string][]Fact{}
	for _, f := range facts {
		by[f.CIK] = append(by[f.CIK], f)
	}
	out := make(map[string]Summary, len(by))
	for cik, fs := range by {
		if s, ok := summarize(fs); ok {
			out[cik] = s
		}
	}
	return out
}

func summarize(fs []Fact) (Summary, bool) {
	of := func(metric string) []Fact {
		var r []Fact
		for _, f := range fs {
			if f.Metric == metric {
				r = append(r, f)
			}
		}
		slices.SortFunc(r, func(a, b Fact) int { return strings.Compare(b.End, a.End) }) // newest first
		return r
	}
	num := map[string]float64{}
	var s Summary

	rev, inc := of(Revenue), of(NetIncome)
	// Revenue and income are shown as one fiscal year, so they must be of the
	// same year: the newest year-end both are reported for. If there is none
	// in common, the newer of the two is kept and the other left out, rather
	// than show the revenue of one year beside the profit of another.
	var r, i *Fact
	for k := range rev {
		for l := range inc {
			if rev[k].End == inc[l].End {
				r, i = &rev[k], &inc[l]
				break
			}
		}
		if r != nil {
			break
		}
	}
	switch {
	case r == nil && len(rev) > 0 && (len(inc) == 0 || rev[0].End >= inc[0].End):
		r = &rev[0]
	case r == nil && len(inc) > 0:
		i = &inc[0]
	}
	if r != nil {
		num[Revenue] = r.Value
		s.End = r.End
		for _, p := range rev {
			if d, ok := daysBetween(p.End, r.End); ok && d >= 340 && d <= 390 {
				if p.Value > 0 {
					num["growth"] = r.Value/p.Value - 1
				}
				break
			}
		}
	}
	if i != nil {
		num[NetIncome] = i.Value
		s.End = i.End
		if r != nil && r.Value > 0 {
			num["margin"] = i.Value / r.Value
		}
	}
	target := s.End
	near := func(metric string) (Fact, bool) {
		best, ok := Fact{}, false
		var bestGap = 46
		for _, f := range of(metric) {
			if d, okd := daysBetween(f.End, target); okd {
				if d < 0 {
					d = -d
				}
				if d < bestGap {
					best, bestGap, ok = f, d, true
				}
			}
		}
		return best, ok
	}
	if a, ok := near(Assets); ok {
		num[Assets] = a.Value
	}
	if e, ok := near(Equity); ok {
		num[Equity] = e.Value
		if e.Value > 0 {
			if v, has := num[NetIncome]; has {
				num["roe"] = v / e.Value
			}
		}
		if a, has := num[Assets]; has && a > 0 {
			num["equity_ratio"] = e.Value / a
		}
	}
	// Market value needs the shares outstanding on the cover page. The average
	// over the year is a different quantity (a company that bought back half its
	// shares has far fewer than the average), so it is kept apart and never
	// stands in for the count.
	if sh := of(Shares); len(sh) > 0 && sh[0].Value > 0 {
		num[Shares] = sh[0].Value
	} else if av := of(AvgShares); len(av) > 0 && av[0].Value > 0 {
		num[AvgShares] = av[0].Value
	}
	if len(num) == 0 {
		return Summary{}, false
	}
	s.Num = num
	return s, true
}
