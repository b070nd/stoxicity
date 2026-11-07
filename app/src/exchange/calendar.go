package exchange

import "time"

// Day is what the calendar says of one date of a market.
type Day struct {
	// Closed is the reason the market is shut all day (a weekend or a holiday's
	// name); empty when it trades.
	Closed string
	// Early: the regular session ends at 13:00 and the extended one after it
	// at 17:00 at the latest.
	Early bool
	// Why names the early close.
	Why string
	// Close is the clock time the regular session ends on an early day; empty
	// means the venue's usual early close.
	Close string
	// Override is the dated, sourced change to the recurring rules that decided
	// the day, nil when the recurring rules did.
	Override *Override
}

// Holiday is a named day the market is closed.
type Holiday struct {
	Date time.Time
	Name string
}

// DayOf is the calendar entry for the date of t in the market's zone. It models
// the US market calendar: weekends, the ten holidays, and the three early closes.
func (i Info) DayOf(t time.Time) Day { return Calendar{}.Day(i, t) }

// recurring is the day by the recurring rules alone.
func (i Info) recurring(t time.Time) Day {
	d := t.In(i.Location())
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return Day{Closed: "weekend"}
	}
	for _, h := range holidaysIn(d.Year(), d.Location()) {
		if sameDate(h.Date, d) {
			return Day{Closed: h.Name}
		}
	}
	y, m, dd := d.Date()
	loc := d.Location()
	switch {
	case m == time.July && dd == 3 && d.Weekday() <= time.Thursday:
		return Day{Early: true, Why: "day before Independence Day"}
	case m == time.December && dd == 24 && d.Weekday() <= time.Thursday:
		return Day{Early: true, Why: "Christmas Eve"}
	case d.Weekday() == time.Friday && sameDate(d, nthWeekday(y, time.November, time.Thursday, 4, loc).AddDate(0, 0, 1)):
		return Day{Early: true, Why: "day after Thanksgiving"}
	}
	return Day{}
}

// Holidays lists the market holidays from the date of t on, up to n of them.
func (i Info) Holidays(t time.Time, n int) []Holiday {
	loc := i.Location()
	from := t.In(loc)
	var out []Holiday
	for y := from.Year(); y <= from.Year()+1 && len(out) < n; y++ {
		for _, h := range holidaysIn(y, loc) {
			if !h.Date.Before(time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)) {
				out = append(out, h)
				if len(out) == n {
					break
				}
			}
		}
	}
	return out
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// holidaysIn are the market holidays whose observed date falls in year, in date
// order.
func holidaysIn(year int, loc *time.Location) []Holiday {
	date := func(m time.Month, d int) time.Time { return time.Date(year, m, d, 0, 0, 0, 0, loc) }
	// A holiday on a Saturday is observed on the Friday before, on a Sunday on
	// the Monday after. New Year's Day on a Saturday is not observed at all: the
	// market trades on the 31st of December.
	observed := func(t time.Time) (time.Time, bool) {
		switch t.Weekday() {
		case time.Saturday:
			return t.AddDate(0, 0, -1), t.Month() != time.January
		case time.Sunday:
			return t.AddDate(0, 0, 1), true
		}
		return t, true
	}
	var out []Holiday
	add := func(t time.Time, name string) {
		if o, ok := observed(t); ok && o.Year() == year {
			out = append(out, Holiday{Date: o, Name: name})
		}
	}
	add(date(time.January, 1), "New Year's Day")
	out = append(out, Holiday{nthWeekday(year, time.January, time.Monday, 3, loc), "Martin Luther King Jr. Day"})
	out = append(out, Holiday{nthWeekday(year, time.February, time.Monday, 3, loc), "Washington's Birthday"})
	out = append(out, Holiday{easter(year, loc).AddDate(0, 0, -2), "Good Friday"})
	out = append(out, Holiday{lastWeekday(year, time.May, time.Monday, loc), "Memorial Day"})
	if year >= 2022 {
		add(date(time.June, 19), "Juneteenth")
	}
	add(date(time.July, 4), "Independence Day")
	out = append(out, Holiday{nthWeekday(year, time.September, time.Monday, 1, loc), "Labor Day"})
	out = append(out, Holiday{nthWeekday(year, time.November, time.Thursday, 4, loc), "Thanksgiving Day"})
	add(date(time.December, 25), "Christmas Day")
	sortHolidays(out)
	return out
}

func sortHolidays(h []Holiday) {
	for i := 1; i < len(h); i++ {
		for j := i; j > 0 && h[j].Date.Before(h[j-1].Date); j-- {
			h[j], h[j-1] = h[j-1], h[j]
		}
	}
}

// nthWeekday is the n-th (from 1) given weekday of a month.
func nthWeekday(year int, m time.Month, wd time.Weekday, n int, loc *time.Location) time.Time {
	first := time.Date(year, m, 1, 0, 0, 0, 0, loc)
	shift := (int(wd) - int(first.Weekday()) + 7) % 7
	return first.AddDate(0, 0, shift+7*(n-1))
}

// lastWeekday is the last given weekday of a month.
func lastWeekday(year int, m time.Month, wd time.Weekday, loc *time.Location) time.Time {
	last := time.Date(year, m+1, 0, 0, 0, 0, 0, loc)
	shift := (int(last.Weekday()) - int(wd) + 7) % 7
	return last.AddDate(0, 0, -shift)
}

// easter is Easter Sunday of the year (the anonymous Gregorian algorithm).
func easter(year int, loc *time.Location) time.Time {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, loc)
}
