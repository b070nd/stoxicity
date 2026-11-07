package marketdata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// Check is where the look-up of one stock's candles has been.
type Check struct {
	CheckedAt time.Time
	Found     int // candles the last look-up brought
	Error     string
}

// Store is the part of the database the keeper needs.
type Store interface {
	PutCandles(ctx context.Context, rows []Candle) error
	// NewestDays returns the newest stored day of each ticker.
	NewestDays(ctx context.Context) (map[string]string, error)
	Checks(ctx context.Context) (map[string]Check, error)
	SaveChecks(ctx context.Context, checks map[string]Check) error
}

// Default ages: a stock looked up lately is not looked up again before the
// next trading day; one the source knows nothing of is tried again in a week.
const (
	DefaultFresh   = 20 * time.Hour
	DefaultMissing = 7 * 24 * time.Hour
	// DefaultBackfill is how many trading days of a market are fetched when none
	// is stored yet; a stock fetched alone gets this many calendar days.
	DefaultBackfill = 7
	DefaultWorkers  = 4
)

// Keeper keeps the candles of stocks up to date.
type Keeper struct {
	Store  Store
	Source Source
	Now    func() time.Time
	// Workers is how many stocks are asked for at once; DefaultWorkers if 0.
	Workers int
	// Splits, when set and when Store keeps splits (SplitStore), is read once a
	// day after the candles: a price move is only explained by a split the store
	// knows of.
	Splits SplitSource
	// Refetch, when above 0, asks again for the market days of the last Refetch
	// calendar days even if candles are stored for them: candles made before they
	// were kept raw are replaced by raw ones.
	Refetch int
	// Pace is the wait between two requests of market days, for a plan that
	// allows few requests a minute (Polygon's free plan allows five).
	Pace time.Duration
}

// RawDayStore is what a Store may also give: the days that have candles which
// are known to be raw, so a refetch does not repeat them.
type RawDayStore interface {
	RawDays(ctx context.Context) (map[string]bool, error)
}

func (k *Keeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// Report says what Refresh did.
type Report struct {
	Asked  int // stocks (or market days) asked for
	Stored int // candles stored
	None   int // stocks the source knows nothing of (or closed days)
	Failed int
	Left   int // stocks (or days) due but not asked because of the budget
	Splits int // splits read (new or not)
}

func (r Report) String() string {
	out := fmt.Sprintf("%d asked, %d candles stored, %d with none, %d failed, %d left", r.Asked, r.Stored, r.None, r.Failed, r.Left)
	if r.Splits > 0 {
		out += fmt.Sprintf(", %d splits read", r.Splits)
	}
	return out
}

// Refresh brings the candles of the stocks up to date, asking for at most
// budget stocks (or, for a source of whole days, budget days), those named in
// priority first and then the rest in ticker order, so the work does not depend
// on how the lists happen to be ordered.
func (k *Keeper) Refresh(ctx context.Context, tickers, priority []string, budget int) (Report, error) {
	if budget <= 0 || k.Source == nil {
		return Report{}, nil
	}
	var rep Report
	var err error
	switch src := k.Source.(type) {
	case PerDay:
		rep, err = k.refreshDays(ctx, src, tickers, budget)
	case PerTicker:
		rep, err = k.refreshStocks(ctx, src, tickers, priority, budget)
	default:
		return Report{}, fmt.Errorf("marketdata: source %q gives neither a stock nor a day", k.Source.Name())
	}
	// The splits are read even when the candles failed to come: they are what
	// tells a price move from a split, and they are cheap (one request a day).
	n, serr := k.refreshSplits(ctx)
	rep.Splits = n
	return rep, errors.Join(err, serr)
}

// SplitsFresh is how long a reading of the splits stands before the next one.
const SplitsFresh = 20 * time.Hour

// splitsFirst is how far back the first reading of the splits goes: far enough
// to hold every split since the oldest candle the store is likely to have.
const splitsFirst = 365 * 24 * time.Hour

// refreshSplits reads the splits when the last reading is older than
// SplitsFresh. A reading starts a week before the last one ended (a split is
// announced and listed late), at least 30 days back, so the readings join
// without a gap.
func (k *Keeper) refreshSplits(ctx context.Context) (int, error) {
	ss, ok := k.Store.(SplitStore)
	if k.Splits == nil || !ok {
		return 0, nil
	}
	cov, err := ss.SplitCoverage(ctx)
	if err != nil {
		return 0, err
	}
	now := k.now()
	since := now.Add(-splitsFirst)
	if !cov.Read.IsZero() {
		if now.Sub(cov.Read) < SplitsFresh {
			return 0, nil
		}
		since = cov.Read.AddDate(0, 0, -7)
		if min := now.AddDate(0, 0, -30); since.After(min) {
			since = min
		}
	}
	got, err := k.Splits.Splits(ctx, since)
	if err != nil {
		// What was read before this error is not stored as a complete reading.
		return 0, fmt.Errorf("marketdata: splits: %w", err)
	}
	if err := ss.PutSplits(context.WithoutCancel(ctx), got, since, now); err != nil {
		return 0, err
	}
	return len(got), nil
}

func (k *Keeper) refreshStocks(ctx context.Context, src PerTicker, tickers, priority []string, budget int) (Report, error) {
	var rep Report
	checks, err := k.Store.Checks(ctx)
	if err != nil {
		return rep, err
	}
	newest, err := k.Store.NewestDays(ctx)
	if err != nil {
		return rep, err
	}
	now := k.now()
	due := func(t string) bool {
		c, ok := checks[t]
		if !ok {
			return true
		}
		age := DefaultFresh
		if c.Found == 0 {
			age = DefaultMissing
		}
		return now.Sub(c.CheckedAt) >= age
	}
	var todo []string
	seen := map[string]bool{}
	add := func(t string) {
		if t != "" && !seen[t] && due(t) {
			seen[t] = true
			todo = append(todo, t)
		}
	}
	known := map[string]bool{}
	for _, t := range tickers {
		known[t] = true
	}
	for _, t := range priority {
		if known[t] {
			add(t)
		}
	}
	for _, t := range slices.Sorted(slices.Values(tickers)) {
		add(t)
	}
	if len(todo) > budget {
		rep.Left = len(todo) - budget
		todo = todo[:budget]
	}

	type answer struct {
		ticker  string
		candles []Candle
		err     error
	}
	workers := k.Workers
	if workers <= 0 {
		workers = DefaultWorkers
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	next := make(chan string)
	answers := make(chan answer)
	ack := make(chan struct{}) // the answer was dealt with: the worker may ask again
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range next {
				since := now.AddDate(0, 0, -DefaultBackfill)
				if d, err := time.Parse(time.DateOnly, newest[t]); err == nil {
					since = d.AddDate(0, 0, 1)
				}
				c, err := src.History(runCtx, t, since)
				select {
				case answers <- answer{t, c, err}:
				case <-runCtx.Done():
					return
				}
				select {
				case <-ack:
				case <-runCtx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(next)
		for _, t := range todo {
			select {
			case next <- t:
			case <-runCtx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(answers) }()

	var rows []Candle
	done := map[string]Check{}
	var pushed error
	for a := range answers {
		var he *httpx.Error
		switch {
		case errors.As(a.err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked):
			if pushed == nil {
				pushed = fmt.Errorf("marketdata: %s is pushing back, stopping: %w", src.Name(), a.err)
				stop()
			}
		case a.err != nil && runCtx.Err() != nil:
		case a.err != nil:
			rep.Asked++
			rep.Failed++
			done[a.ticker] = Check{CheckedAt: now, Error: a.err.Error()}
		default:
			rep.Asked++
			rows = append(rows, a.candles...)
			if len(a.candles) == 0 {
				rep.None++
				// A stock already known that has no new candle today is not "none".
				if newest[a.ticker] != "" {
					done[a.ticker] = Check{CheckedAt: now, Found: 1}
					rep.None--
					break
				}
			}
			done[a.ticker] = Check{CheckedAt: now, Found: len(a.candles)}
		}
		select {
		case ack <- struct{}{}:
		case <-runCtx.Done():
		}
	}
	sctx := context.WithoutCancel(ctx)
	if err := k.Store.PutCandles(sctx, rows); err != nil {
		return rep, err
	}
	rep.Stored = len(rows)
	if err := k.Store.SaveChecks(sctx, done); err != nil {
		return rep, err
	}
	rep.Left += len(todo) - rep.Asked
	if pushed != nil {
		return rep, pushed
	}
	return rep, ctx.Err()
}

// tradingDays are the weekdays from..to, oldest first. The exchanges' holidays
// are not listed: a holiday is asked for once and the source answers with no
// candles, which is remembered.
func tradingDays(from, to time.Time) []time.Time {
	var out []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			out = append(out, d)
		}
	}
	return out
}

func (k *Keeper) refreshDays(ctx context.Context, src PerDay, tickers []string, budget int) (Report, error) {
	var rep Report
	checks, err := k.Store.Checks(ctx)
	if err != nil {
		return rep, err
	}
	newest, err := k.Store.NewestDays(ctx)
	if err != nil {
		return rep, err
	}
	// The last day that is over: yesterday, New York's calendar.
	ny, _ := time.LoadLocation("America/New_York")
	if ny == nil {
		ny = time.UTC
	}
	local := k.now().In(ny)
	last := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	first := last.AddDate(0, 0, -DefaultBackfill)
	if k.Refetch > 0 {
		first = last.AddDate(0, 0, -k.Refetch+1)
	} else {
		for _, d := range newest {
			if t, err := time.Parse(time.DateOnly, d); err == nil && t.AddDate(0, 0, 1).After(first) {
				first = t.AddDate(0, 0, 1)
			}
		}
	}
	var raw map[string]bool
	if rs, ok := k.Store.(RawDayStore); ok && k.Refetch > 0 {
		if raw, err = rs.RawDays(ctx); err != nil {
			return rep, err
		}
	}
	var days []time.Time
	for _, d := range tradingDays(first, last) {
		c, read := checks["day:"+d.Format(time.DateOnly)]
		// A day read before is not asked again, except in a refetch, which only
		// leaves out the days that had no candles (holidays) and those already raw.
		if (!read || k.Refetch > 0 && c.Found > 0) && !raw[d.Format(time.DateOnly)] {
			days = append(days, d)
		}
	}
	if len(days) > budget {
		rep.Left = len(days) - budget
		days = days[:budget]
	}
	ours := make(map[string]string, len(tickers))
	for _, t := range tickers {
		ours[NormTicker(t)] = t
	}
	done := map[string]Check{}
	var rows []Candle
	var runErr error
	for i, d := range days {
		if ctx.Err() != nil {
			rep.Left += len(days) - rep.Asked
			break
		}
		if i > 0 && k.Pace > 0 {
			select {
			case <-time.After(k.Pace):
			case <-ctx.Done():
			}
		}
		candles, err := src.Day(ctx, d)
		var he *httpx.Error
		switch {
		case errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked):
			rep.Left += len(days) - rep.Asked
			runErr = fmt.Errorf("marketdata: %s is pushing back, stopping: %w", src.Name(), err)
		case err != nil:
			rep.Asked++
			rep.Failed++
			runErr = errors.Join(runErr, fmt.Errorf("%s: %w", d.Format(time.DateOnly), err))
			continue
		default:
			rep.Asked++
			if len(candles) == 0 {
				rep.None++
				done["day:"+d.Format(time.DateOnly)] = Check{CheckedAt: k.now()} // a holiday
				continue
			}
			for _, c := range candles {
				if t, ok := ours[NormTicker(c.Ticker)]; ok {
					c.Ticker = t
					rows = append(rows, c)
				}
			}
			done["day:"+d.Format(time.DateOnly)] = Check{CheckedAt: k.now(), Found: len(candles)}
			continue
		}
		break
	}
	sctx := context.WithoutCancel(ctx)
	if err := k.Store.PutCandles(sctx, rows); err != nil {
		return rep, err
	}
	rep.Stored = len(rows)
	if err := k.Store.SaveChecks(sctx, done); err != nil {
		return rep, err
	}
	if runErr == nil {
		runErr = ctx.Err()
	}
	return rep, runErr
}

// Tidy removes blanks from a ticker list and puts it in ticker order without
// repeats.
func Tidy(list []string) []string {
	var out []string
	for _, t := range list {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
