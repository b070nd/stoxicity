package exchange

import (
	"fmt"
	"strconv"
	"time"
)

// Status says whether a market is trading at an instant.
type Status struct {
	// Open: some session, regular or extended, is running.
	Open bool
	// Session is the one running, or the next to start when closed.
	Session string
	// Core: the running session is the regular one.
	Core bool
	// Change is when the running session ends, or when the next one starts.
	Change time.Time
	// Closed is why the market is shut today (weekend, a holiday), empty when it
	// trades today.
	Closed string
	// Early: today closes early.
	Early bool
}

// Describe is the status in a few words: "open · regular, closes in 2h10m".
func (s Status) Describe(now time.Time) string {
	in := s.Change.Sub(now).Round(time.Minute)
	if s.Open {
		kind := s.Session
		if s.Early {
			kind += ", early close"
		}
		return fmt.Sprintf("open · %s, ends in %s", kind, span(in))
	}
	why := ""
	if s.Closed != "" {
		why = " (" + s.Closed + ")"
	}
	return fmt.Sprintf("closed%s · %s opens in %s", why, s.Session, span(in))
}

// span writes a duration as "2h10m", "45m" or "3d4h".
func span(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	h, m := int(d/time.Hour), int(d/time.Minute)%60
	switch {
	case h >= 24:
		return strconv.Itoa(h/24) + "d" + strconv.Itoa(h%24) + "h"
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// at is the clock time "15:04" on the date of day, in loc.
func at(day time.Time, clock string, loc *time.Location) time.Time {
	t, _ := time.ParseInLocation("15:04", clock, loc)
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, loc)
}

// Window is a session on a date, with the instants it starts and ends.
type Window struct {
	Session
	From, To time.Time
}

// SessionsOn are the sessions of the date of t, trimmed for an early close;
// none when the market is shut that day.
func (i Info) SessionsOn(t time.Time) []Window {
	loc := i.Location()
	day := t.In(loc)
	cal := i.DayOf(day)
	if cal.Closed != "" {
		return nil
	}
	var out []Window
	coreEnd := ""
	for _, s := range i.Sessions {
		if s.Core {
			coreEnd = s.To
		}
	}
	early := at(day, i.earlyClose(), loc)
	for _, s := range i.Sessions {
		w := Window{Session: s, From: at(day, s.From, loc), To: at(day, s.To, loc)}
		if cal.Early {
			switch {
			case s.Core:
				w.To = early
			case s.From >= coreEnd: // the session after the close starts at the early close and runs four hours at most
				w.From = early
				w.To = minTime(w.To, early.Add(4*time.Hour))
			}
		}
		if w.To.After(w.From) {
			out = append(out, w)
		}
	}
	return out
}

// earlyClose is the clock time the regular session ends on an early-close day.
func (i Info) earlyClose() string {
	if i.Zone == "America/Chicago" {
		return "12:00"
	}
	return "13:00"
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// StatusAt is the status of the market at now.
func (i Info) StatusAt(now time.Time) Status {
	loc := i.Location()
	local := now.In(loc)
	today := i.DayOf(local)
	st := Status{Closed: today.Closed, Early: today.Early}
	for _, w := range i.SessionsOn(local) {
		if !local.Before(w.From) && local.Before(w.To) {
			st.Open, st.Session, st.Core, st.Change = true, w.Name, w.Core, w.To
			return st
		}
	}
	for d := 0; d < 14; d++ {
		for _, w := range i.SessionsOn(local.AddDate(0, 0, d)) {
			if w.From.After(local) {
				st.Session, st.Change = w.Name, w.From
				return st
			}
		}
	}
	return st
}

// Normal is the sessions of an ordinary trading day on the date of t, whatever
// the calendar says of that date: the hours to show someone, not whether the
// market is open.
func (i Info) Normal(t time.Time) []Window {
	loc := i.Location()
	day := t.In(loc)
	out := make([]Window, len(i.Sessions))
	for k, s := range i.Sessions {
		out[k] = Window{Session: s, From: at(day, s.From, loc), To: at(day, s.To, loc)}
	}
	return out
}

// EarlyClose is the clock time the regular session ends on an early-close day,
// "13:00" for the markets on the US east coast.
func (i Info) EarlyClose() string { return i.earlyClose() }
