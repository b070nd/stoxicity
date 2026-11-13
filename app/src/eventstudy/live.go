package eventstudy

import "time"

// ContextFor measures the conditions around an event that may be too new to
// have an entry session in the data: a signal is judged when the news arrives,
// before the session it would be entered on.
//
// It uses the bars before the entry session, exactly as Run does. When that
// session is not in the data yet, every bar the series has is before it, and
// all of them are used, provided the series is current: its last bar may be at
// most the study's max_entry_gap_days before the information. A series that
// ends earlier is stale, and the conditions are unknown, not carried forward.
func ContextFor(spec Spec, o Observation, prices Prices) (Context, error) {
	if err := spec.Validate(); err != nil {
		return unknownContext(spec.Context), err
	}
	ses, err := newSessions(spec.Session)
	if err != nil {
		return unknownContext(spec.Context), err
	}
	if prices == nil {
		return unknownContext(spec.Context), nil
	}
	s, ok := prices.Series(o.Security)
	if !ok || len(s.Bars) == 0 || s.Validate() != nil {
		return unknownContext(spec.Context), nil
	}
	bars := s.Bars
	e, found := entryIndex(bars, o, spec.Entry, time.Duration(spec.EntryDelayMinutes)*time.Minute, ses)
	if !found {
		gap, ok := daysBetween(bars[len(bars)-1].Day, availableDay(o, ses))
		if !ok || gap > spec.MaxEntryGapDays {
			return unknownContext(spec.Context), nil
		}
		e = len(bars)
	}
	var market []Bar
	marketEntry := -1
	if spec.Benchmark != "" {
		if m, ok := prices.Series(spec.Benchmark); ok && len(m.Bars) > 0 && m.Validate() == nil {
			market = m.Bars
			switch {
			case e < len(bars):
				if i, ok := indexOfDay(market, bars[e].Day); ok {
					marketEntry = i
				}
			case market[len(market)-1].Day == bars[len(bars)-1].Day:
				// The entry session is ahead of both series, which end on the same day.
				marketEntry = len(market)
			}
		}
	}
	return contextAt(spec.Context, bars, e, market, marketEntry, o.MarketCap), nil
}
