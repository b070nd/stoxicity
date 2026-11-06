// Package status is what every screen and every meter says about the health of
// the sources. A Source is one source as a reader sees it: its health level,
// what it is doing, when it last worked and when it is next due. Two adapters
// fill it: FromStored reads what the scheduler persisted (the full-screen
// interface, a different process from the one that polls), FromBoard reads the
// live Board of the process that polls. Meters adds up a list of Sources, and
// Sort puts the ones that need attention first.
package status

import (
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/scheduler"
)

// Level is how well a source is doing.
type Level string

const (
	// Unknown: the source has not been polled yet.
	Unknown Level = ""
	Green   Level = "green"
	Yellow  Level = "yellow"
	Red     Level = "red"
)

// State is what a source is doing.
type State string

const (
	// Off: the source is switched off in the configuration.
	Off State = "off"
	// Idle: nothing to do until Until.
	Idle State = "idle"
	// Running: a poll is in flight since Since.
	Running State = "running"
	// Waiting: due since Since, but waiting for a worker or for its host.
	Waiting State = "waiting"
	// Resting: due, but its host is cooling down until Until.
	Resting State = "resting"
	// Parked: nothing polls it until it is refreshed (a 404, a parse failure).
	Parked State = "parked"
)

// Source is one source as a reader sees it.
type Source struct {
	ID      string
	Kind    string
	Licence string
	Host    string
	Enabled bool

	Level Level
	State State
	// Late: the timer ran out while the poll was still running.
	Late bool

	LastOK time.Time // zero before the first good poll
	// Since is when the state began (Running, Waiting); Until is when the next
	// poll is due (Idle) or the host may be tried again (Resting). Zero
	// otherwise.
	Since time.Time
	Until time.Time

	Failures  int
	LastNew   int // articles stored by the last poll (live adapter only)
	LastError string
}

// Needs is how urgently the source needs a look: 0 is the most urgent. Sources
// that are off come last, those never polled before the healthy ones.
func (s Source) Needs() int {
	if !s.Enabled || s.State == Off {
		return 5
	}
	switch s.Level {
	case Red:
		return 0
	case Yellow:
		return 1
	case Unknown:
		return 3
	}
	return 4
}

// Sort puts the sources that need attention first, then by ID.
func Sort(rows []Source) {
	slices.SortStableFunc(rows, func(a, b Source) int {
		if d := a.Needs() - b.Needs(); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// level maps a scheduler grade to a Level; a source that has neither worked nor
// failed has not been polled, whatever the grade says.
func level(grade string, lastOK time.Time, failures int) Level {
	if lastOK.IsZero() && failures == 0 {
		return Unknown
	}
	// The scheduler writes its levels in capitals, the readers use lower case.
	switch strings.ToLower(grade) {
	case "green":
		return Green
	case "red":
		return Red
	}
	return Yellow
}

// FromStored reads a source from what the scheduler persisted. interval is how
// often the source is polled, which the grade needs: a source that has not
// worked for more than two intervals is yellow however it ended its last poll.
func FromStored(src model.Source, h scheduler.Health, interval time.Duration, now time.Time) Source {
	s := Source{
		ID: src.ID, Kind: string(src.Kind), Licence: string(src.Licence), Enabled: src.Enabled,
		State: Idle, LastOK: h.LastOK, Failures: h.Failures, LastError: h.LastError,
	}
	switch {
	case !src.Enabled:
		s.State = Off
	case h.Permanent:
		s.State = Parked
	case !h.NextDue.IsZero():
		s.Until = h.NextDue
	}
	s.Level = level(scheduler.Grade(h, interval, now), h.LastOK, h.Failures)
	return s
}

// FromBoard reads the sources from the live Board of the process that polls.
// reg supplies what the board does not carry (kind, licence, switched on); a
// source missing from it is taken as enabled with those left empty.
func FromBoard(b scheduler.Board, reg map[string]model.Source) []Source {
	out := make([]Source, 0, len(b.Rows))
	at := make(map[string]int, len(b.Rows))
	for _, a := range b.Rows {
		if a.State == scheduler.Late {
			// The same source as its running row, a second time: say so there.
			if i, ok := at[a.SourceID]; ok {
				out[i].Late = true
			}
			continue
		}
		s := Source{
			ID: a.SourceID, Host: a.Host, Enabled: true,
			LastOK: a.LastOK, Failures: a.Failures, LastNew: a.LastNew, LastError: a.LastError,
			Level: level(a.Level, a.LastOK, a.Failures),
		}
		if src, ok := reg[a.SourceID]; ok {
			s.Kind, s.Licence, s.Enabled = string(src.Kind), string(src.Licence), src.Enabled
		}
		switch a.State {
		case scheduler.Running:
			s.State, s.Since = Running, a.Since
		case scheduler.Waiting:
			s.State, s.Since = Waiting, a.Since
		case scheduler.Resting:
			s.State, s.Since, s.Until = Resting, a.Since, a.Until
		default:
			s.State, s.Until = Idle, a.Until
			if a.Parked {
				s.State = Parked
			}
		}
		at[s.ID] = len(out)
		out = append(out, s)
	}
	return out
}

// Meters is the sources added up, for a header: how many are in each health
// level and what they are doing.
type Meters struct {
	Total int // sources that are switched on
	Off   int // sources switched off, not counted in Total

	Green, Yellow, Red, Unknown int

	Running, Waiting, Resting, Parked, Late int

	// Next is the idle source that is due soonest, and when.
	Next    string
	NextDue time.Time
}

// Summarise adds up the sources.
func Summarise(rows []Source) Meters {
	var m Meters
	for _, s := range rows {
		if !s.Enabled || s.State == Off {
			m.Off++
			continue
		}
		m.Total++
		switch s.Level {
		case Green:
			m.Green++
		case Yellow:
			m.Yellow++
		case Red:
			m.Red++
		default:
			m.Unknown++
		}
		switch s.State {
		case Running:
			m.Running++
		case Waiting:
			m.Waiting++
		case Resting:
			m.Resting++
		case Parked:
			m.Parked++
		case Idle:
			if !s.Until.IsZero() && (m.Next == "" || s.Until.Before(m.NextDue)) {
				m.Next, m.NextDue = s.ID, s.Until
			}
		}
		if s.Late {
			m.Late++
		}
	}
	return m
}
