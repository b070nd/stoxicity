package marketdata

import (
	"math"
	"testing"
	"time"
)

func calendarCandle(day string, close float64) Candle {
	return Candle{Ticker: "TEST", Day: day, Open: close, High: close, Low: close, Close: close, Raw: true, Currency: "USD"}
}

func TestPriceCalendarCalendarMonthsGapsAndFutureRows(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	rows := []Candle{
		calendarCandle("2026-11-30", 999), calendarCandle("2026-11-06", 120),
		calendarCandle("2025-11-28", 100), calendarCandle("2025-12-15", 105), calendarCandle("2025-12-31", 110),
		calendarCandle("2026-01-30", 110), calendarCandle("2026-02-27", 99), calendarCandle("2026-04-30", 108),
	}
	c := BuildPriceCalendar(at, rows, nil, SplitCoverage{})
	if c.Months[0].Month != "2025-12" || c.Months[11].Month != "2026-11" || c.Months[11].Close != 120 {
		t.Fatalf("wrong window: %+v", c)
	}
	for i, want := range []float64{.1, 0, -.1} {
		if !c.Months[i].HasChange || math.Abs(c.Months[i].Change-want) > 1e-12 {
			t.Errorf("month %d: %+v", i, c.Months[i])
		}
	}
	if c.Months[3].Day != "" || c.Months[4].HasChange || c.Months[4].Close != 108 {
		t.Fatalf("missing month filled or bridged: %+v", c.Months[3:5])
	}
	if rows[0].Close != 999 || rows[2].Close != 100 {
		t.Fatal("input mutated")
	}
	// A March 31 anchor must not skip February while selecting calendar months.
	leap := BuildPriceCalendar(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), nil, nil, SplitCoverage{})
	if leap.Months[0].Month != "2023-04" || leap.Months[10].Month != "2024-02" || leap.Months[11].Month != "2024-03" {
		t.Fatalf("month rollover: %+v", leap)
	}
}

func TestPriceCalendarReconcilesSplitsAndRefusesUnknownBases(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	rows := []Candle{calendarCandle("2025-11-28", 100), calendarCandle("2025-12-31", 100), calendarCandle("2026-01-30", 50), calendarCandle("2026-02-27", 60)}
	splits := []Split{{Ticker: "TEST", Day: "2026-01-02", From: 1, To: 2}}
	c := BuildPriceCalendar(at, rows, splits, SplitCoverage{})
	if c.Months[0].Close != 50 || !c.Months[1].HasChange || c.Months[1].Change != 0 || math.Abs(c.Months[2].Change-.2) > 1e-12 {
		t.Fatalf("phantom split loss: %+v", c.Months[:3])
	}
	rows[1].Raw = false
	c = BuildPriceCalendar(at, rows, splits, SplitCoverage{})
	if !c.Months[0].Unresolved || c.Months[0].HasChange || c.Months[1].HasChange || c.Months[1].Close != 50 {
		t.Fatalf("guessed an unknown basis: %+v", c.Months[:2])
	}
	rows[1].Raw = true
	c = BuildPriceCalendar(at, rows, nil, SplitCoverage{})
	if !c.Months[0].Unresolved || !c.Months[1].Unresolved || c.Months[1].HasChange {
		t.Fatalf("unexplained split presented as performance: %+v", c.Months[:2])
	}
	c = BuildPriceCalendar(at, rows, nil, SplitCoverage{Since: "2025-01-01", Read: at})
	if !c.Months[1].HasChange || c.Months[1].Change != -.5 {
		t.Fatalf("verified large real move hidden: %+v", c.Months[1])
	}
	rows[0].Currency = "GBP"
	c = BuildPriceCalendar(at, rows, splits, SplitCoverage{})
	if c.Months[0].HasChange {
		t.Fatal("compared different currency bases")
	}
}

func TestPriceCalendarTenYearsUsePreviousYearClosesAndKeepGaps(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	rows := []Candle{
		calendarCandle("2015-12-31", 999), calendarCandle("2016-12-30", 100),
		calendarCandle("2017-06-30", 105), calendarCandle("2017-12-29", 110),
		calendarCandle("2018-12-31", 110), calendarCandle("2019-12-31", 99),
		calendarCandle("2021-12-31", 108), calendarCandle("2025-12-31", 100),
		calendarCandle("2026-11-06", 110), calendarCandle("2026-12-31", 999),
	}
	c := BuildPriceCalendar(at, rows, nil, SplitCoverage{})
	if c.Years[0].Year != "2017" || c.Years[9].Year != "2026" || c.Years[9].Close != 110 {
		t.Fatalf("wrong annual window: %+v", c.Years)
	}
	for i, want := range []float64{.1, 0, -.1} {
		if !c.Years[i].HasChange || math.Abs(c.Years[i].Change-want) > 1e-12 {
			t.Errorf("year %d: %+v", i, c.Years[i])
		}
	}
	if c.Years[3].Day != "" || c.Years[4].HasChange || c.Years[4].Close != 108 {
		t.Fatalf("missing year bridged: %+v", c.Years[3:5])
	}
	if !c.Years[9].HasChange || math.Abs(c.Years[9].Change-.1) > 1e-12 {
		t.Fatalf("YTD baseline: %+v", c.Years[9])
	}
	// Calendar-year rollover changes the window even before new prices arrive.
	c = BuildPriceCalendar(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), rows[:len(rows)-1], nil, SplitCoverage{})
	if c.Years[0].Year != "2018" || c.Years[9].Year != "2027" || c.Years[9].Day != "" {
		t.Fatalf("year rollover: %+v", c.Years)
	}
}

func TestPriceCalendarAnnualSplitDoesNotBecomeAFalseLoss(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	rows := []Candle{calendarCandle("2016-12-30", 100), calendarCandle("2017-12-29", 100), calendarCandle("2018-12-31", 50)}
	splits := []Split{{Ticker: "TEST", Day: "2018-01-02", From: 1, To: 2}}
	c := BuildPriceCalendar(at, rows, splits, SplitCoverage{})
	if c.Years[0].Close != 50 || !c.Years[1].HasChange || c.Years[1].Change != 0 {
		t.Fatalf("false annual split loss: %+v", c.Years[:2])
	}
	rows[1].Raw = false
	c = BuildPriceCalendar(at, rows, splits, SplitCoverage{})
	if !c.Years[0].Unresolved || c.Years[1].HasChange {
		t.Fatalf("unknown annual basis guessed: %+v", c.Years[:2])
	}
}
