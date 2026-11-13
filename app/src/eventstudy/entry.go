package eventstudy

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sessions turns a trading day into the times the exchange opened and closed.
type sessions struct {
	loc                *time.Location
	openMin, closeMin  int
	openText, closeTxt string
}

func clock(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("eventstudy: session time %q is not HH:MM", s)
	}
	return hh*60 + mm, nil
}

func newSessions(s Session) (sessions, error) {
	if s.Zone == "" {
		return sessions{}, errors.New("eventstudy: the session needs a time zone")
	}
	loc, err := time.LoadLocation(s.Zone)
	if err != nil {
		return sessions{}, fmt.Errorf("eventstudy: session zone %q is not known", s.Zone)
	}
	o, err := clock(s.Open)
	if err != nil {
		return sessions{}, err
	}
	c, err := clock(s.Close)
	if err != nil {
		return sessions{}, err
	}
	if c <= o {
		return sessions{}, errors.New("eventstudy: the session must close after it opens")
	}
	return sessions{loc: loc, openMin: o, closeMin: c, openText: s.Open, closeTxt: s.Close}, nil
}

func (s sessions) at(day string, minutes int) (time.Time, bool) {
	d, err := time.ParseInLocation(time.DateOnly, day, s.loc)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(d.Year(), d.Month(), d.Day(), minutes/60, minutes%60, 0, 0, s.loc), true
}

func (s sessions) open(day string) (time.Time, bool)  { return s.at(day, s.openMin) }
func (s sessions) close(day string) (time.Time, bool) { return s.at(day, s.closeMin) }

// entryIndex is the bar a position is opened on: the first whose open (for an
// entry at the open) or close (for an entry at the close) lies strictly after
// the information plus the delay. An intraday entry is made inside a session, so
// the session is the first that closes after that time. Information known only
// to the day is usable from the first session after that day, whichever the
// rule. False when the bars end before such a session.
func entryIndex(bars []Bar, o Observation, rule Entry, delay time.Duration, ses sessions) (int, bool) {
	if o.DayPrecision {
		day := o.AvailableAt.UTC().Format(time.DateOnly)
		i := sort.Search(len(bars), func(i int) bool { return bars[i].Day > day })
		return i, i < len(bars)
	}
	usable := o.AvailableAt.Add(delay)
	// No session can qualify before the calendar day the information is usable
	// on in the exchange's zone, less a day for zones ahead of it.
	from := usable.In(ses.loc).AddDate(0, 0, -1).Format(time.DateOnly)
	for i := sort.Search(len(bars), func(i int) bool { return bars[i].Day >= from }); i < len(bars); i++ {
		var at time.Time
		var ok bool
		if rule == NextClose || rule == Intraday {
			at, ok = ses.close(bars[i].Day)
		} else {
			at, ok = ses.open(bars[i].Day)
		}
		if ok && at.After(usable) {
			return i, true
		}
	}
	return 0, false
}

// availableDay is the calendar day, in the exchange's zone, the information
// was available on.
func availableDay(o Observation, ses sessions) string {
	if o.DayPrecision {
		return o.AvailableAt.UTC().Format(time.DateOnly)
	}
	return o.AvailableAt.In(ses.loc).Format(time.DateOnly)
}

// daysBetween counts the calendar days from one day to a later one; false when
// either is not a day.
func daysBetween(from, to string) (int, bool) {
	a, err1 := time.Parse(time.DateOnly, from)
	b, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return int(b.Sub(a).Hours() / 24), true
}

// missingWeekdays counts the Monday-to-Friday days strictly between two days.
func missingWeekdays(from, to string) int {
	a, err1 := time.Parse(time.DateOnly, from)
	b, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil {
		return 0
	}
	n := 0
	for d := a.AddDate(0, 0, 1); d.Before(b); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// maxMissingWeekdays is how many weekdays may lie between two consecutive bars
// of a holding period: one, for a holiday. More means sessions are missing.
const maxMissingWeekdays = 1

// indexOfDay is the index of the bar of a day; false when the series has none.
func indexOfDay(bars []Bar, day string) (int, bool) {
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Day >= day })
	return i, i < len(bars) && bars[i].Day == day
}

// block names the run of sessions an entry day falls in, for a holding period
// of that many sessions. Events entered in one block overlap in time and are
// resampled together. Blocks are counted in weekdays from a fixed Monday, so
// they do not depend on the data.
func block(day string, sessions int) string {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil || sessions < 1 {
		return day
	}
	d := int(t.Unix()/86400) - 4 // 1970-01-05 was a Monday
	week, dow := d/7, d%7
	if dow > 4 {
		dow = 4
	}
	return strconv.Itoa((week*5 + dow) / sessions)
}
