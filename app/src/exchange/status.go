package exchange

import (
	"fmt"
	"strconv"
	"strings"
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
	// Scheduled is always true: the status is read from the venue's schedule and
	// the calendar. Nothing here observes the market, so an open status is not a
	// statement that trading is available; Halt carries what was observed.
	Scheduled bool
	// Override is the calendar override that decided today, nil when the
	// recurring rules did.
	Override *Override
	// CalendarVersion names the calendar the status was read from.
	CalendarVersion string
	// Halt is an observed halt or outage covering the venue now, with its source;
	// nil when none is recorded (which is not the same as none happening).
	Halt *Observation
}

// Caveat says what the status does not establish.
func (s Status) Caveat() string {
	var parts []string
	if s.Override != nil {
		parts = append(parts, fmt.Sprintf("calendar override for %s: %s (%s)", s.Override.Date, s.Override.Reason, s.Override.Source))
	}
	if s.Halt != nil {
		parts = append(parts, fmt.Sprintf("observed %s since %s (%s)", s.Halt.Kind, s.Halt.From.UTC().Format("2006-01-02 15:04Z"), s.Halt.Source))
	} else {
		parts = append(parts, "schedule only: trading halts and outages are not observed, so this is not confirmation that trading is available")
	}
	if s.CalendarVersion != "" {
		parts = append(parts, "calendar "+s.CalendarVersion)
	}
	return strings.Join(parts, " · ")
}

// Describe is the status in a few words: "open · regular, closes in 2h10m".
func (s Status) Describe(now time.Time) string {
	in := s.Change.Sub(now).Round(time.Minute)
	if s.Halt != nil {
		return fmt.Sprintf("halted (%s) · scheduled %s, next change in %s", s.Halt.Kind, strings.ToLower(s.Session), span(in))
	}
	if s.Open {
		kind := s.Session
		if s.Early {
			kind += ", early close"
		}
		return fmt.Sprintf("scheduled open · %s, ends in %s", kind, span(in))
	}
	why := ""
	if s.Closed != "" {
		why = " (" + s.Closed + ")"
	}
	return fmt.Sprintf("scheduled closed%s · %s opens in %s", why, s.Session, span(in))
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
func (i Info) SessionsOn(t time.Time) []Window { return Calendar{}.SessionsOn(i, t) }

// SessionsOn are the sessions of the date of t as this calendar has it.
func (c Calendar) SessionsOn(i Info, t time.Time) []Window {
	loc := i.Location()
	day := t.In(loc)
	cal := c.Day(i, day)
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
	closeAt := i.earlyClose()
	if cal.Close != "" {
		closeAt = cal.Close
	}
	early := at(day, closeAt, loc)
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
func (i Info) StatusAt(now time.Time) Status { return Calendar{}.StatusAt(i, now) }

// StatusAt is the status of the market at now as this calendar has it: the
// scheduled status, with the override that decided today and any halt or outage
// recorded for the venue. A recorded halt does not close the schedule; it is
// reported beside it.
func (c Calendar) StatusAt(i Info, now time.Time) Status {
	loc := i.Location()
	local := now.In(loc)
	today := c.Day(i, local)
	st := Status{Closed: today.Closed, Early: today.Early, Scheduled: true, Override: today.Override, CalendarVersion: c.Version}
	st.Halt = c.haltAt(i, now)
	for _, w := range c.SessionsOn(i, local) {
		if !local.Before(w.From) && local.Before(w.To) {
			st.Open, st.Session, st.Core, st.Change = true, w.Name, w.Core, w.To
			return st
		}
	}
	for d := 0; d < 14; d++ {
		for _, w := range c.SessionsOn(i, local.AddDate(0, 0, d)) {
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
