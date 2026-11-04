// Package timeline builds the two timeline views from articles: bars per day
// (relevant against total) and an event list ordered by time. The same code
// serves the home, stock, topic and domain pages; callers pass only the
// articles that belong to the page.
package timeline

import (
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Day is one bar.
type Day struct {
	Date     time.Time // midnight at the start of the day, in the location given to Daily
	Total    int
	Relevant int
}

// Daily returns one Day for every calendar day from through to, inclusive,
// including days without articles, so gaps are visible. from and to are
// calendar dates: only their year, month and day are used. Articles are
// assigned to the day they were published in loc, which should be the market's
// time zone, because a filing made at 9 pm New York time belongs to that day
// and not to the next one in UTC. Articles outside the range are ignored.
func Daily(articles []model.Article, threshold float64, from, to time.Time, loc *time.Location) []Day {
	if loc == nil {
		loc = time.UTC
	}
	start, end := date(from, loc), date(to, loc)
	if end.Before(start) {
		return nil
	}
	var days []Day
	index := map[string]int{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		index[d.Format(time.DateOnly)] = len(days)
		days = append(days, Day{Date: d})
	}
	for _, a := range articles {
		i, ok := index[a.Published.In(loc).Format(time.DateOnly)]
		if !ok {
			continue
		}
		days[i].Total++
		if a.Score.Relevant(threshold) {
			days[i].Relevant++
		}
	}
	return days
}

// Event is one line of the event list.
type Event struct {
	At      time.Time
	Kind    model.EventKind
	Article model.Article
}

// Events returns the relevant articles newest first.
func Events(articles []model.Article, threshold float64) []Event {
	var out []Event
	for _, a := range articles {
		if a.Score.Relevant(threshold) {
			out = append(out, Event{At: a.Published, Kind: a.Score.Kind, Article: a})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// date is midnight at the start of t's calendar date, in loc.
func date(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
