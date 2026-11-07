package exchange

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// FileName is the calendar file in the configuration folder.
const FileName = "exchange_calendar.yaml"

// Override kinds.
const (
	// OverrideClosed: the venue is shut all day although the recurring rules say it
	// trades (a day of mourning, a storm, a technical failure announced in advance).
	OverrideClosed = "closed"
	// OverrideEarly: the regular session ends at Close instead of the usual time.
	OverrideEarly = "early"
	// OverrideOpen: the venue trades on a day the recurring rules shut it (a
	// special session), with the regular session ending at Close if that is given.
	OverrideOpen = "open"
)

// Override is a dated, sourced change to the recurring calendar for one venue.
// Without a source and the time it was recorded it is not used: a calendar change
// that nobody can trace is not a fact.
type Override struct {
	// Venue is the exchange as Lookup names it, or "*" for every venue.
	Venue string `yaml:"venue"`
	// Date is the day in the venue's own time zone (YYYY-MM-DD).
	Date string `yaml:"date"`
	Kind string `yaml:"kind"`
	// Close is the clock time ("13:00") the regular session ends, for early and open.
	Close  string `yaml:"close,omitempty"`
	Reason string `yaml:"reason"`
	// Source is where the change was announced: a notice's address or reference.
	Source string `yaml:"source"`
	// Recorded is when this calendar learned of it. A calendar read as of an earlier
	// time does not have it.
	Recorded time.Time `yaml:"recorded"`
}

// Observation is something seen of a venue's operation, as opposed to its
// schedule: a trading halt or an outage, with where it was seen.
type Observation struct {
	Venue string `yaml:"venue"`
	// Kind is halt, outage or closure.
	Kind string    `yaml:"kind"`
	From time.Time `yaml:"from"`
	// To is when it ended; zero while it is on.
	To       time.Time `yaml:"to,omitempty"`
	Source   string    `yaml:"source"`
	Recorded time.Time `yaml:"recorded"`
}

// Calendar is a version of the venue calendar: the recurring rules of this
// package plus dated, sourced overrides and observations. The zero value is the
// recurring rules alone.
type Calendar struct {
	// Version names this set of overrides; a status says which it was read from.
	Version      string        `yaml:"version"`
	Overrides    []Override    `yaml:"overrides,omitempty"`
	Observations []Observation `yaml:"observations,omitempty"`
}

// Validate says what is wrong with the calendar, nil when nothing is. Every
// override needs a known venue, a date, a kind, a reason, a source and the time
// it was recorded; an early or open day may state when the regular session ends.
func (c Calendar) Validate() error {
	var errs []error
	for n, o := range c.Overrides {
		fail := func(f string, a ...any) {
			errs = append(errs, fmt.Errorf("override %d (%s %s): %s", n+1, o.Venue, o.Date, fmt.Sprintf(f, a...)))
		}
		if _, ok := venueOf(o.Venue); !ok {
			fail("the venue is not known")
		}
		if _, err := time.Parse(time.DateOnly, o.Date); err != nil {
			fail("the date is not YYYY-MM-DD")
		}
		if !slices.Contains([]string{OverrideClosed, OverrideEarly, OverrideOpen}, o.Kind) {
			fail("the kind must be closed, early or open")
		}
		if o.Close != "" {
			if _, err := time.Parse("15:04", o.Close); err != nil {
				fail("close is not HH:MM")
			}
		}
		if o.Kind == OverrideEarly && o.Close == "" {
			fail("an early close needs the time")
		}
		if strings.TrimSpace(o.Reason) == "" || strings.TrimSpace(o.Source) == "" {
			fail("a reason and a source are required")
		}
		if o.Recorded.IsZero() {
			fail("the time it was recorded is required")
		}
	}
	for n, o := range c.Observations {
		if _, ok := venueOf(o.Venue); !ok || o.From.IsZero() || o.Source == "" || o.Recorded.IsZero() || !slices.Contains([]string{"halt", "outage", "closure"}, o.Kind) {
			errs = append(errs, fmt.Errorf("observation %d: venue, kind (halt, outage or closure), from, source and recorded are required", n+1))
		}
	}
	return errors.Join(errs...)
}

// venueOf resolves a venue name; "*" is every venue.
func venueOf(name string) (Info, bool) {
	if name == "*" {
		return Info{}, true
	}
	return Lookup(name)
}

func (o Override) applies(i Info, date string) bool {
	if o.Date != date {
		return false
	}
	if o.Venue == "*" {
		return true
	}
	v, ok := Lookup(o.Venue)
	return ok && v.Name == i.Name
}

// AsOf returns the calendar as it was known at the time: only the overrides and
// observations recorded by then, under the same version name. The zero time is now.
func (c Calendar) AsOf(known time.Time) Calendar {
	if known.IsZero() {
		return c
	}
	out := Calendar{Version: c.Version}
	for _, o := range c.Overrides {
		if !o.Recorded.After(known) {
			out.Overrides = append(out.Overrides, o)
		}
	}
	for _, o := range c.Observations {
		if !o.Recorded.After(known) {
			out.Observations = append(out.Observations, o)
		}
	}
	return out
}

// Day is the calendar entry for the date of t: the recurring rules, replaced by
// the newest override recorded for that venue and date. An override that is not
// valid is ignored.
func (c Calendar) Day(i Info, t time.Time) Day {
	d := i.recurring(t)
	date := t.In(i.Location()).Format(time.DateOnly)
	var hit *Override
	for k := range c.Overrides {
		o := c.Overrides[k]
		if !o.applies(i, date) || o.Source == "" || o.Reason == "" || o.Recorded.IsZero() {
			continue
		}
		if hit == nil || o.Recorded.After(hit.Recorded) {
			hit = &o
		}
	}
	if hit == nil {
		return d
	}
	switch hit.Kind {
	case OverrideClosed:
		return Day{Closed: hit.Reason, Override: hit}
	case OverrideEarly:
		return Day{Early: true, Why: hit.Reason, Close: hit.Close, Override: hit}
	case OverrideOpen:
		out := Day{Override: hit}
		if hit.Close != "" {
			out.Early, out.Why, out.Close = true, hit.Reason, hit.Close
		}
		return out
	}
	return d
}

// haltAt is the observation of a halt, outage or closure covering the venue at
// now, the newest if several.
func (c Calendar) haltAt(i Info, now time.Time) *Observation {
	var hit *Observation
	for k := range c.Observations {
		o := c.Observations[k]
		if !(o.Venue == "*" || strings.EqualFold(o.Venue, i.Name)) || now.Before(o.From) || (!o.To.IsZero() && !now.Before(o.To)) {
			continue
		}
		if hit == nil || o.Recorded.After(hit.Recorded) {
			hit = &o
		}
	}
	return hit
}

// Load reads the calendar file of the configuration folder. A missing file is the
// recurring rules alone (version "recurring-rules"); an invalid one is an error,
// never a half-applied calendar.
func Load(dir string) (Calendar, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return Calendar{Version: "recurring-rules"}, nil
	}
	if err != nil {
		return Calendar{}, err
	}
	var c Calendar
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Calendar{}, fmt.Errorf("exchange: %s: %w", FileName, err)
	}
	if c.Version == "" {
		return Calendar{}, fmt.Errorf("exchange: %s: a version is required", FileName)
	}
	if err := c.Validate(); err != nil {
		return Calendar{}, fmt.Errorf("exchange: %s: %w", FileName, err)
	}
	return c, nil
}
