package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// PolygonAggsURL is Polygon's aggregate bars of one ticker over a range of
// days, the same API as the grouped daily bars PolygonURL names: the address
// is this, the ticker, "/range/", the bar length, the timespan and the two days.
const PolygonAggsURL = "https://api.polygon.io/v2/aggs/ticker/"

// The regular session of the US exchanges, in New York time, in minutes from
// midnight. Bars outside it (the pre-market and after-hours trading Polygon
// also returns) are not kept: an entry is only ever made inside a session, and
// the study's own session setting decides which part of these it uses.
const (
	regularOpenMin  = 9*60 + 30
	regularCloseMin = 16 * 60
)

// intradayLimit is the most bars Polygon returns in one answer; a longer range
// is followed through the next-page address, up to intradayPages pages.
const (
	intradayLimit = 50000
	intradayPages = 5
)

// IntradayChunkDays is the longest run of calendar days one request asks for,
// and IntradayGapDays the longest stretch of unwanted days a request spans
// between two wanted ones: beyond it, two requests are cheaper than a long one.
const (
	IntradayChunkDays = 28
	IntradayGapDays   = 7
)

// IntradaySource gives the intraday bars of one stock.
type IntradaySource interface {
	Source
	// Intraday returns the bars of the given length of ticker that started on
	// the days from to to (YYYY-MM-DD, inclusive, the exchange's calendar),
	// oldest first; none (and no error) when the source knows nothing of the
	// stock in those days.
	Intraday(ctx context.Context, ticker string, interval time.Duration, from, to string) ([]IntradayBar, error)
}

// PolygonIntraday is Polygon's intraday bars. It needs the same key as Polygon.
type PolygonIntraday struct {
	Client httpx.Client
	Key    string
	// URL replaces PolygonAggsURL (it ends with a slash); tests point it at a
	// server.
	URL string
}

// Name implements Source.
func (PolygonIntraday) Name() string { return "polygon" }

// polygonSymbol is how Polygon spells a ticker: a class share is BRK.B there,
// where the program may hold BRK-B.
func polygonSymbol(ticker string) string {
	return strings.ReplaceAll(NormTicker(ticker), "-", ".")
}

// Intraday implements IntradaySource. It asks for raw (unadjusted) bars and
// keeps those that started inside the regular session on a day from from to to.
func (p PolygonIntraday) Intraday(ctx context.Context, ticker string, interval time.Duration, from, to string) ([]IntradayBar, error) {
	if p.Key == "" {
		return nil, errors.New("marketdata: Polygon needs an API key (STOXICITY_POLYGON_KEY)")
	}
	if interval < time.Minute || interval%time.Minute != 0 || interval > time.Hour {
		return nil, fmt.Errorf("marketdata: intraday bars of %s are not a whole number of minutes between 1 and 60", interval)
	}
	if strings.TrimSpace(ticker) == "" || strings.ContainsAny(ticker, "/\\?#\r\n\x00") {
		return nil, errors.New("marketdata: intraday: invalid ticker")
	}
	for _, d := range []string{from, to} {
		if _, err := time.Parse(time.DateOnly, d); err != nil {
			return nil, fmt.Errorf("marketdata: intraday: %q is not a day in the form YYYY-MM-DD", d)
		}
	}
	loc, err := time.LoadLocation(IntradayZone)
	if err != nil {
		return nil, fmt.Errorf("marketdata: intraday: the exchange zone %s is not known", IntradayZone)
	}
	base := p.URL
	if base == "" {
		base = PolygonAggsURL
	}
	next := base + url.PathEscape(polygonSymbol(ticker)) + "/range/" + strconv.Itoa(int(interval/time.Minute)) + "/minute/" + from + "/" + to + "?" +
		url.Values{"adjusted": {"false"}, "sort": {"asc"}, "limit": {strconv.Itoa(intradayLimit)}, "apiKey": {p.Key}}.Encode()
	var out []IntradayBar
	for page := 0; page < intradayPages && next != ""; page++ {
		resp, err := p.Client.Do(ctx, httpx.Request{URL: next})
		if err != nil {
			return nil, err
		}
		var ans struct {
			Results []struct {
				T int64   `json:"t"`
				O float64 `json:"o"`
				H float64 `json:"h"`
				L float64 `json:"l"`
				C float64 `json:"c"`
				V float64 `json:"v"`
			} `json:"results"`
			Next string `json:"next_url"`
		}
		if err := json.Unmarshal(resp.Body, &ans); err != nil {
			return nil, fmt.Errorf("marketdata: read Polygon's intraday answer for %s: %w", ticker, err)
		}
		for _, r := range ans.Results {
			start := time.UnixMilli(r.T).UTC()
			local := start.In(loc)
			minute := local.Hour()*60 + local.Minute()
			day := local.Format(time.DateOnly)
			if minute < regularOpenMin || minute >= regularCloseMin || day < from || day > to {
				continue
			}
			bar := IntradayBar{Ticker: ticker, Start: start, Interval: interval, Open: r.O, High: r.H, Low: r.L, Close: r.C,
				Volume: int64(r.V), Raw: true, Source: p.Name()}
			if bar.Valid() {
				out = append(out, bar)
			}
		}
		next = ""
		if ans.Next != "" {
			// The next page's address does not carry the key.
			next = ans.Next
			if !strings.Contains(next, "apiKey=") {
				sep := "?"
				if strings.Contains(next, "?") {
					sep = "&"
				}
				next += sep + url.Values{"apiKey": {p.Key}}.Encode()
			}
			if page == intradayPages-1 {
				return out, fmt.Errorf("marketdata: Polygon has more intraday bars of %s than %d pages hold", ticker, intradayPages)
			}
		}
	}
	return out, nil
}

// IntradayCheck says that the source was asked for the bars of one ticker and
// bar length on one day, and how many it gave.
type IntradayCheck struct {
	Ticker   string
	Interval time.Duration
	// Day is YYYY-MM-DD, the exchange's calendar.
	Day       string
	Bars      int
	CheckedAt time.Time
}

// IntradayStore is the part of the database the intraday keeper needs.
type IntradayStore interface {
	PutIntradayBars(ctx context.Context, bars []IntradayBar) error
	// IntradayChecked returns, by ticker, the days already asked for at the bar
	// length.
	IntradayChecked(ctx context.Context, interval time.Duration) (map[string]map[string]bool, error)
	PutIntradayChecks(ctx context.Context, checks []IntradayCheck) error
}

// IntradayRequest is one request to the source: the bars of one ticker over a
// run of days, of which only the Days are wanted and kept.
type IntradayRequest struct {
	Ticker   string
	From, To string
	Days     []string
}

// IntradayPlan is the requests a wish for intraday bars comes to.
type IntradayPlan struct {
	Requests []IntradayRequest
	// Days is how many ticker-days are to be asked for, and Covered how many
	// more were wanted but have been asked for before.
	Days, Covered int
}

// IntradayDays lists the weekdays from from to to (YYYY-MM-DD, inclusive). The
// exchanges' holidays are not listed: a holiday is asked for once and the
// source answers with no bars, which is remembered.
func IntradayDays(from, to string) ([]string, error) {
	a, err1 := time.Parse(time.DateOnly, from)
	b, err2 := time.Parse(time.DateOnly, to)
	if err := errors.Join(err1, err2); err != nil {
		return nil, fmt.Errorf("marketdata: intraday days: %w", err)
	}
	var out []string
	for _, d := range tradingDays(a, b) {
		out = append(out, d.Format(time.DateOnly))
	}
	return out, nil
}

// IntradayKeeper keeps the intraday bars of stocks, for the days asked for.
type IntradayKeeper struct {
	Store  IntradayStore
	Source IntradaySource
	Now    func() time.Time
	// Pace is the wait between two requests, for a plan that allows few requests
	// a minute. The provider's request budget is the quota the HTTP client holds.
	Pace time.Duration
}

func (k *IntradayKeeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// Plan turns the days wanted of each ticker (YYYY-MM-DD) into requests, leaving
// out the days already asked for at the bar length unless refetch is set. A
// ticker's days are put in order; a request spans at most IntradayChunkDays
// calendar days and no more than IntradayGapDays between two wanted days.
// Tickers come in ticker order, so the plan does not depend on map order.
func (k *IntradayKeeper) Plan(ctx context.Context, wanted map[string][]string, interval time.Duration, refetch bool) (IntradayPlan, error) {
	var plan IntradayPlan
	checked := map[string]map[string]bool{}
	if !refetch {
		var err error
		if checked, err = k.Store.IntradayChecked(ctx, interval); err != nil {
			return plan, err
		}
	}
	tickers := make([]string, 0, len(wanted))
	for t := range wanted {
		tickers = append(tickers, t)
	}
	sort.Strings(tickers)
	for _, t := range tickers {
		days := append([]string(nil), wanted[t]...)
		sort.Strings(days)
		var cur *IntradayRequest
		prev := ""
		for i, d := range days {
			if i > 0 && d == days[i-1] {
				continue
			}
			if checked[t][d] {
				plan.Covered++
				continue
			}
			plan.Days++
			if gap, ok := daysFrom(prev, d); cur != nil && ok {
				if span, ok2 := daysFrom(cur.From, d); ok2 && gap <= IntradayGapDays && span < IntradayChunkDays {
					cur.To, cur.Days = d, append(cur.Days, d)
					prev = d
					continue
				}
			}
			plan.Requests = append(plan.Requests, IntradayRequest{Ticker: t, From: d, To: d, Days: []string{d}})
			cur = &plan.Requests[len(plan.Requests)-1]
			prev = d
		}
	}
	return plan, nil
}

// daysFrom counts the calendar days from one day to a later one; false when
// either is not a day.
func daysFrom(from, to string) (int, bool) {
	a, err1 := time.Parse(time.DateOnly, from)
	b, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return int(b.Sub(a).Hours() / 24), true
}

// IntradayReport says what a fetch did.
type IntradayReport struct {
	Asked  int // requests made
	Stored int // bars stored
	None   int // requests that brought no bar
	Failed int
	Left   int // requests planned but not made, because of the budget or a stop
}

func (r IntradayReport) String() string {
	return fmt.Sprintf("%d asked, %d bars stored, %d with none, %d failed, %d left", r.Asked, r.Stored, r.None, r.Failed, r.Left)
}

// Fetch makes at most budget of the requests, in order, storing what each
// brings as soon as it comes, so that a stop loses nothing already asked for.
// It stops at once when the source or the request budget pushes back (a
// throttled or blocked answer, or the quota the HTTP client holds running
// out); the requests not made are counted in Left. A bar outside the days of its
// request is not kept. A request that fails is not recorded as asked.
func (k *IntradayKeeper) Fetch(ctx context.Context, requests []IntradayRequest, interval time.Duration, budget int) (IntradayReport, error) {
	var rep IntradayReport
	if budget > 0 && len(requests) > budget {
		rep.Left = len(requests) - budget
		requests = requests[:budget]
	}
	loc, err := time.LoadLocation(IntradayZone)
	if err != nil {
		return rep, fmt.Errorf("marketdata: intraday: the exchange zone %s is not known", IntradayZone)
	}
	var runErr error
	for i, req := range requests {
		if err := ctx.Err(); err != nil {
			rep.Left += len(requests) - i
			return rep, err
		}
		if i > 0 && k.Pace > 0 {
			select {
			case <-time.After(k.Pace):
			case <-ctx.Done():
				rep.Left += len(requests) - i
				return rep, ctx.Err()
			}
		}
		bars, err := k.Source.Intraday(ctx, req.Ticker, interval, req.From, req.To)
		var he *httpx.Error
		if errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked) {
			rep.Left += len(requests) - i
			return rep, fmt.Errorf("marketdata: %s is pushing back, stopping: %w", k.Source.Name(), err)
		}
		if err != nil {
			rep.Asked++
			rep.Failed++
			runErr = errors.Join(runErr, fmt.Errorf("%s %s to %s: %w", req.Ticker, req.From, req.To, err))
			continue
		}
		rep.Asked++
		wantedDay := map[string]bool{}
		for _, d := range req.Days {
			wantedDay[d] = true
		}
		counts := map[string]int{}
		var keep []IntradayBar
		for _, b := range bars {
			if day := b.Start.In(loc).Format(time.DateOnly); wantedDay[day] {
				counts[day]++
				keep = append(keep, b)
			}
		}
		if len(keep) == 0 {
			rep.None++
		}
		checks := make([]IntradayCheck, 0, len(req.Days))
		for _, d := range req.Days {
			checks = append(checks, IntradayCheck{Ticker: req.Ticker, Interval: interval, Day: d, Bars: counts[d], CheckedAt: k.now()})
		}
		sctx := context.WithoutCancel(ctx)
		if err := k.Store.PutIntradayBars(sctx, keep); err != nil {
			return rep, err
		}
		if err := k.Store.PutIntradayChecks(sctx, checks); err != nil {
			return rep, err
		}
		rep.Stored += len(keep)
	}
	return rep, runErr
}
