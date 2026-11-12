package workspace

import (
	"context"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

// PriceCalendars reads stored monthly and yearly endpoints. The read-only snapshot is
// cached across filtering keystrokes and refreshed like the latest quotes.
func (w *Workspace) PriceCalendars() (map[string]marketdata.PriceCalendar, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	at := w.now().In(w.zone)
	day := at.Format("2006-01-02")
	if w.priceCalendars != nil && w.priceCalendarDay == day && !at.Before(w.priceCalendarsAt) && at.Sub(w.priceCalendarsAt) < quotesEvery {
		return w.priceCalendars, w.priceCalendarErr
	}
	w.priceCalendars, w.priceCalendarErr = map[string]marketdata.PriceCalendar{}, nil
	w.priceCalendarsAt, w.priceCalendarDay = at, day
	ctx := context.Background()
	monthlyFrom := marketdata.PriceCalendarStart(at).AddDate(0, -1, 0).Format("2006-01-02")
	from := marketdata.PriceYearsStart(at).AddDate(-1, 0, 0).Format("2006-01-02")
	rows, err := w.db.CalendarCandles(ctx, from, monthlyFrom, day)
	if err != nil {
		w.priceCalendarErr = err
		return w.priceCalendars, err
	}
	splits, err := w.db.Splits(ctx)
	if err != nil {
		w.priceCalendarErr = err
		return w.priceCalendars, err
	}
	coverage, err := w.db.SplitCoverage(ctx)
	if err != nil {
		w.priceCalendarErr = err
		return w.priceCalendars, err
	}
	byTicker := map[string][]marketdata.Split{}
	for _, split := range splits {
		key := marketdata.NormTicker(split.Ticker)
		byTicker[key] = append(byTicker[key], split)
	}
	closesByTicker := map[string][]marketdata.Candle{}
	for ticker, closes := range rows {
		key := marketdata.NormTicker(ticker)
		closesByTicker[key] = append(closesByTicker[key], closes...)
	}
	for key, closes := range closesByTicker {
		w.priceCalendars[key] = marketdata.BuildPriceCalendar(at, closes, byTicker[key], coverage)
	}
	histories, err := w.db.CalendarHistories(ctx, from[:7], monthlyFrom[:7], day[:7])
	if err != nil {
		w.priceCalendarErr = err
		return w.priceCalendars, err
	}
	for ticker, history := range histories {
		w.priceCalendars[marketdata.NormTicker(ticker)] = marketdata.BuildHistoryCalendar(at, history)
	}
	return w.priceCalendars, nil
}
