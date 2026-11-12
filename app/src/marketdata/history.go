package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

const YahooHistoryURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

var ErrNoHistory = errors.New("no public price history for this symbol")

type HistoryMonth struct {
	Month string
	Close float64
}

// PriceHistory is one coherent split-adjusted, price-only monthly snapshot.
// It is deliberately separate from raw daily candles and dividend-adjusted returns.
type PriceHistory struct {
	Ticker, Symbol, Currency, LatestDay string
	FetchedAt                           time.Time
	Months                              []HistoryMonth
}

type HistorySource interface {
	History(context.Context, string) (PriceHistory, error)
}

type YahooHistory struct {
	Client httpx.Client
	URL    string // test/mirror base; ends with a slash
	Now    func() time.Time
}

func (y YahooHistory) History(ctx context.Context, ticker string) (PriceHistory, error) {
	base := y.URL
	if base == "" {
		base = YahooHistoryURL
	}
	symbol := strings.ToUpper(strings.TrimSpace(ticker))
	if symbol == "" || strings.ContainsAny(symbol, "/\\?#\r\n\x00") {
		return PriceHistory{}, fmt.Errorf("history: invalid symbol")
	}
	now := time.Now()
	if y.Now != nil {
		now = y.Now()
	}
	first := time.Date(now.Year()-10, time.January, 1, 0, 0, 0, 0, time.UTC)
	query := url.Values{"period1": {fmt.Sprint(first.Unix())}, "period2": {fmt.Sprint(now.Unix())}, "interval": {"1mo"}, "includeAdjustedClose": {"false"}}
	response, err := y.Client.Do(ctx, httpx.Request{URL: base + url.PathEscape(symbol) + "?" + query.Encode()})
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			return PriceHistory{}, ErrNoHistory
		}
		return PriceHistory{}, err
	}
	var payload struct {
		Chart struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
			Result []struct {
				Meta struct {
					Symbol, Currency, ExchangeTimezoneName, DataGranularity string
					RegularMarketTime                                       int64
				}
				Timestamp  []int64
				Indicators struct{ Quote []struct{ Close []*float64 } }
			} `json:"result"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		return PriceHistory{}, fmt.Errorf("history: invalid chart JSON: %w", err)
	}
	if payload.Chart.Error != nil {
		if payload.Chart.Error.Code == "Not Found" {
			return PriceHistory{}, ErrNoHistory
		}
		return PriceHistory{}, fmt.Errorf("history: chart provider reported an error")
	}
	if len(payload.Chart.Result) != 1 {
		return PriceHistory{}, fmt.Errorf("history: expected one chart result")
	}
	r := payload.Chart.Result[0]
	if !strings.EqualFold(r.Meta.Symbol, symbol) || r.Meta.Currency == "" || r.Meta.DataGranularity != "1mo" || r.Meta.RegularMarketTime <= 0 || len(r.Indicators.Quote) != 1 || len(r.Timestamp) != len(r.Indicators.Quote[0].Close) || len(r.Timestamp) > 140 {
		return PriceHistory{}, fmt.Errorf("history: inconsistent chart identity or monthly series")
	}
	zone, err := time.LoadLocation(r.Meta.ExchangeTimezoneName)
	if err != nil {
		return PriceHistory{}, fmt.Errorf("history: unknown exchange time zone")
	}
	h := PriceHistory{Ticker: ticker, Symbol: symbol, Currency: r.Meta.Currency, LatestDay: time.Unix(r.Meta.RegularMarketTime, 0).In(zone).Format(time.DateOnly), FetchedAt: now}
	byMonth, stamps := map[string]float64{}, map[string]int64{}
	for i, stamp := range r.Timestamp {
		close := r.Indicators.Quote[0].Close[i]
		if close == nil {
			continue
		}
		if stamp <= 0 || *close <= 0 || math.IsNaN(*close) || math.IsInf(*close, 0) {
			return PriceHistory{}, fmt.Errorf("history: invalid monthly close")
		}
		month := time.Unix(stamp, 0).In(zone).Format("2006-01")
		if month > h.LatestDay[:7] {
			return PriceHistory{}, fmt.Errorf("history: future monthly observation")
		}
		if stamp > stamps[month] {
			byMonth[month], stamps[month] = *close, stamp
		}
	}
	keys := make([]string, 0, len(byMonth))
	for month := range byMonth {
		keys = append(keys, month)
	}
	sort.Strings(keys)
	for _, month := range keys {
		h.Months = append(h.Months, HistoryMonth{Month: month, Close: byMonth[month]})
	}
	if len(h.Months) == 0 {
		return PriceHistory{}, fmt.Errorf("history: empty monthly series")
	}
	if y.Now != nil {
		h.FetchedAt = y.Now()
	} else {
		h.FetchedAt = time.Now()
	}
	return h, nil
}

type HistoryState struct {
	Ticker                           string
	CheckedAt, FetchedAt, LeaseUntil time.Time
	Months                           int
	Problem                          string
}

type HistoryStore interface {
	HistoryStates(context.Context) (map[string]HistoryState, error)
	ClaimHistory(context.Context, string, time.Time, time.Time) (bool, error)
	PutPriceHistory(context.Context, PriceHistory) error
	HistoryFailure(context.Context, string, time.Time, string) error
}

type HistoryReport struct{ Asked, Stored, Failed, Left int }

func (r HistoryReport) String() string {
	return fmt.Sprintf("%d asked, %d stored, %d failed, %d left", r.Asked, r.Stored, r.Failed, r.Left)
}

type HistoryKeeper struct {
	Store  HistoryStore
	Source HistorySource
	Now    func() time.Time
}

// Refresh resumes from durable per-symbol checks. Missing snapshots come before
// routine refreshes, with followed/interesting tickers first within each group.
func (k *HistoryKeeper) Refresh(ctx context.Context, tickers, priority []string, budget int, force bool) (HistoryReport, error) {
	var rep HistoryReport
	states, err := k.Store.HistoryStates(ctx)
	if err != nil {
		return rep, err
	}
	now := time.Now()
	if k.Now != nil {
		now = k.Now()
	}
	ordered, seen, known := []string{}, map[string]bool{}, map[string]bool{}
	for _, t := range tickers {
		known[t] = true
	}
	for _, list := range [][]string{priority, Tidy(tickers)} {
		for _, t := range list {
			if known[t] && !seen[t] {
				seen[t] = true
				ordered = append(ordered, t)
			}
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return states[ordered[i]].FetchedAt.IsZero() && !states[ordered[j]].FetchedAt.IsZero()
	})
	var todo []string
	for _, t := range ordered {
		s := states[t]
		age := 24 * time.Hour
		if s.Problem != "" {
			age = time.Hour
			if s.Problem == ErrNoHistory.Error() {
				age = 7 * 24 * time.Hour
			}
		}
		if !now.Before(s.LeaseUntil) && (force || s.CheckedAt.IsZero() || now.Sub(s.CheckedAt) >= age) {
			todo = append(todo, t)
		}
	}
	if budget > 0 && len(todo) > budget {
		rep.Left = len(todo) - budget
		todo = todo[:budget]
	}
	var problems []error
	for i, ticker := range todo {
		if err := ctx.Err(); err != nil {
			rep.Left += len(todo) - i
			return rep, err
		}
		if k.Now != nil {
			now = k.Now()
		} else {
			now = time.Now()
		}
		claimed, err := k.Store.ClaimHistory(ctx, ticker, states[ticker].CheckedAt, now)
		if err != nil {
			return rep, err
		}
		if !claimed {
			continue
		}
		rep.Asked++
		history, err := k.Source.History(ctx, ticker)
		if err == nil {
			err = k.Store.PutPriceHistory(context.WithoutCancel(ctx), history)
		}
		if err != nil {
			rep.Failed++
			var he *httpx.Error
			checked := now
			if ctx.Err() != nil || (errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked)) {
				checked = states[ticker].CheckedAt
			}
			if saveErr := k.Store.HistoryFailure(context.WithoutCancel(ctx), ticker, checked, err.Error()); saveErr != nil {
				return rep, saveErr
			}
			problems = append(problems, fmt.Errorf("%s: %w", ticker, err))
			if ctx.Err() != nil || (errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked)) {
				rep.Left += len(todo) - i
				break
			}
		} else {
			rep.Stored++
		}
	}
	return rep, errors.Join(problems...)
}

// Run fills a bounded batch immediately and on each interval. Targets are read
// afresh, so newly discovered stocks join the durable, resumable queue.
func (k *HistoryKeeper) Run(ctx context.Context, targets func(context.Context) ([]string, []string, error), every time.Duration, budget int, report func(HistoryReport, error)) {
	timer := time.NewTicker(every)
	defer timer.Stop()
	for {
		all, priority, err := targets(ctx)
		var rep HistoryReport
		if err == nil {
			rep, err = k.Refresh(ctx, all, priority, budget, false)
		}
		if report != nil {
			report(rep, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
