package marketdata

import (
	"math"
	"time"
)

// PricePeriod is a calendar period's last stored close, on the latest split basis.
// An empty Day means no observation, never a zero price or an unchanged period.
type PricePeriod struct {
	Day        string
	Close      float64
	Change     float64
	HasChange  bool
	Unresolved bool
	Note       string
}

type PriceMonth struct {
	Month string
	PricePeriod
}

type PriceYear struct {
	Year string
	PricePeriod
}

// PriceCalendar covers twelve calendar months and ten calendar years, including
// the current month/year to date.
// It describes price performance only, not dividends or a currency conversion.
type PriceCalendar struct {
	Months [12]PriceMonth
	// Years holds ten entries and is read-only, like the cached snapshot.
	Years    []PriceYear
	Currency string
}

// PriceCalendarStart is the first visible month; using day one avoids AddDate's
// end-of-month normalization (for example March 31 minus one month).
func PriceCalendarStart(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, at.Location()).AddDate(0, -11, 0)
}

// PriceYearsStart is the start of the first of ten visible calendar years.
func PriceYearsStart(at time.Time) time.Time {
	return time.Date(at.Year()-9, time.January, 1, 0, 0, 0, 0, at.Location())
}

// BuildPriceCalendar accepts month/year-end observations plus the previous
// period's baseline. Missing periods are not filled forward. Known splits reconcile raw
// closes; unknown bases and unexplained split-like jumps are left unresolved.
func BuildPriceCalendar(at time.Time, closes []Candle, splits []Split, coverage SplitCoverage) PriceCalendar {
	return buildPriceCalendar(at, closes, splits, coverage, false)
}

// BuildHistoryCalendar consumes a coherent split-adjusted monthly snapshot.
// Endpoint dates label calendar periods; they are not claimed trading sessions.
func BuildHistoryCalendar(at time.Time, history PriceHistory) PriceCalendar {
	if _, err := time.Parse(time.DateOnly, history.LatestDay); err != nil {
		return buildPriceCalendar(at, nil, nil, SplitCoverage{}, true)
	}
	var endpoints []Candle
	for _, month := range history.Months {
		start, err := time.Parse("2006-01", month.Month)
		if err != nil {
			continue
		}
		end := start.AddDate(0, 1, -1).Format(time.DateOnly)
		if month.Month == history.LatestDay[:7] {
			end = history.LatestDay
		}
		if end > at.Format(time.DateOnly) {
			continue
		}
		endpoints = append(endpoints, Candle{Ticker: history.Ticker, Day: end, Open: month.Close, High: month.Close, Low: month.Close, Close: month.Close, Currency: history.Currency})
	}
	return buildPriceCalendar(at, endpoints, nil, SplitCoverage{}, true)
}

func buildPriceCalendar(at time.Time, closes []Candle, splits []Split, coverage SplitCoverage, adjusted bool) PriceCalendar {
	start, yearsStart := PriceCalendarStart(at), PriceYearsStart(at)
	months, currency := buildPricePeriods(at, start, 12, false, closes, splits, coverage, adjusted)
	years, yearCurrency := buildPricePeriods(at, yearsStart, 10, true, closes, splits, coverage, adjusted)
	calendar := PriceCalendar{Currency: currency, Years: make([]PriceYear, 10)}
	if calendar.Currency == "" {
		calendar.Currency = yearCurrency
	}
	for i, point := range months {
		calendar.Months[i] = PriceMonth{Month: start.AddDate(0, i, 0).Format("2006-01"), PricePeriod: point}
	}
	for i, point := range years {
		calendar.Years[i] = PriceYear{Year: yearsStart.AddDate(i, 0, 0).Format("2006"), PricePeriod: point}
	}
	return calendar
}

func buildPricePeriods(at, start time.Time, count int, annual bool, closes []Candle, splits []Split, coverage SplitCoverage, adjusted bool) ([]PricePeriod, string) {
	yearStep, monthStep, layout, previous := 0, 1, "2006-01", "no previous-month close"
	if annual {
		yearStep, monthStep, layout, previous = 1, 0, "2006", "no previous-year close"
	}
	first, last := start.AddDate(-yearStep, -monthStep, 0).Format(layout), at.Format(time.DateOnly)
	byMonth := map[string]Candle{}
	var latest Candle
	for _, c := range closes {
		if _, err := time.Parse(time.DateOnly, c.Day); err != nil || !c.Valid() || math.IsNaN(c.Close) || math.IsInf(c.Close, 0) {
			continue
		}
		month := c.Day[:len(layout)]
		if month < first || c.Day > last {
			continue
		}
		if old := byMonth[month]; old.Day < c.Day || (old.Day == c.Day && c.Ticker < old.Ticker) {
			byMonth[month] = c
		}
		if c.Day > latest.Day || (c.Day == latest.Day && c.Ticker < latest.Ticker) {
			latest = c
		}
	}
	adjust := func(c Candle) (float64, string) {
		if c.Day == "" {
			return 0, "no stored close"
		}
		if c.Currency != latest.Currency {
			return 0, "currency basis differs"
		}
		price := c.Close
		if adjusted {
			return price, ""
		}
		for _, split := range splitsBetween(splits, c.Day, last, c.Ticker) {
			if _, err := time.Parse(time.DateOnly, split.Day); err != nil {
				return 0, "invalid split record"
			}
			if !c.Raw {
				return 0, "split basis unresolved"
			}
			if split.From <= 0 || split.To <= 0 {
				return 0, "invalid split ratio"
			}
			price /= split.Ratio()
		}
		if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return 0, "split basis unresolved"
		}
		return price, ""
	}
	points := make([]PricePeriod, count)
	for i := range points {
		month := start.AddDate(yearStep*i, monthStep*i, 0)
		m := PricePeriod{}
		cur := byMonth[month.Format(layout)]
		m.Day = cur.Day
		m.Close, m.Note = adjust(cur)
		m.Unresolved = cur.Day != "" && m.Close == 0
		prev := byMonth[month.AddDate(-yearStep, -monthStep, 0).Format(layout)]
		base, why := adjust(prev)
		if m.Close > 0 && base > 0 {
			ratio := m.Close / base
			if !adjusted && (ratio > MaxMove || ratio < 1/MaxMove || LooksLikeSplit(ratio)) && !coverage.Covers(prev.Day, cur.Day) {
				m.Note = "split-like change unresolved"
			} else {
				m.Change, m.HasChange = ratio-1, true
			}
		} else if m.Close > 0 {
			if prev.Day == "" {
				m.Note = previous
			} else {
				m.Note = why
			}
		}
		points[i] = m
	}
	// A suspected, unexplained split makes relative heights misleading as well.
	// Keep the observed closes readable, but render the strip as unresolved.
	for _, m := range points {
		if m.Note == "split-like change unresolved" {
			for i := range points {
				if points[i].Close > 0 {
					points[i].HasChange = false
					points[i].Unresolved = true
					points[i].Note = "split basis unresolved"
				}
			}
			break
		}
	}
	return points, latest.Currency
}
