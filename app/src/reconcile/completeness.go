package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

// Completeness says how much of a range of days has been read, from what is
// stored and not from whether the last requests worked: a run that stopped, a
// day the SEC had not published, a failed request and a day read for other forms
// than the ones selected now are all gaps, and a day is counted resolved only
// when its stored reading says so.
type Completeness struct {
	From, To string
	Days     int
	// Complete days were read after they were over; NonFiling ones have none by the
	// calendar or an empty completed quarter.
	Complete, NonFiling int
	// Partial days were read too early, Unpublished had no index when asked, Failed
	// had a failed request, Unread were never asked for.
	Partial, Unpublished, Failed, Unread int
	// FormsStale are complete days that were read for other forms than the selected
	// ones (or for an unrecorded selection): they are resolved for what they were
	// read for and are read again for the rest.
	FormsStale int
	// Gaps lists the days that are not resolved for the selected forms, oldest
	// first, with why.
	Gaps []Gap
}

// Gap is one day that is not resolved.
type Gap struct {
	Day    string
	Status string
	Why    string
}

// Resolved is the days that need no further reading for the selected forms.
func (c Completeness) Resolved() int { return c.Days - len(c.Gaps) }

// Percent is the share of days resolved, 0 to 100.
func (c Completeness) Percent() float64 {
	if c.Days == 0 {
		return 100
	}
	return 100 * float64(c.Resolved()) / float64(c.Days)
}

// Measure reads the stored days of [from, to] against the selected forms.
func Measure(ctx context.Context, db Index, from, to time.Time, forms []string) (Completeness, error) {
	c := Completeness{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly)}
	stored, err := db.IndexDays(ctx, c.From, c.To)
	if err != nil {
		return c, err
	}
	want := FormsKey(forms)
	for d := day(from); !d.After(day(to)); d = d.AddDate(0, 0, 1) {
		c.Days++
		key := d.Format(time.DateOnly)
		h, ok := stored[key]
		switch {
		case !ok:
			c.Unread++
			c.Gaps = append(c.Gaps, Gap{Day: key, Status: "unread", Why: "never asked for"})
			continue
		case h.Status == store.DayPartial:
			c.Partial++
		case h.Status == store.DayUnpublished:
			c.Unpublished++
		case h.Status == store.DayError:
			c.Failed++
		case h.Status == store.DayComplete:
			c.Complete++
		case h.Status == store.DayNonFiling:
			c.NonFiling++
		}
		if !covered(h, want) {
			why := h.Basis
			switch {
			case h.Status.Resolved():
				c.FormsStale++
				why = fmt.Sprintf("read for forms %q, not for %q", h.Forms, want)
			case h.LastError != "":
				why += ": " + h.LastError
			}
			c.Gaps = append(c.Gaps, Gap{Day: key, Status: string(h.Status), Why: why})
		}
	}
	return c, nil
}
