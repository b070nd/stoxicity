package reconcile

import (
	"context"
	"testing"

	"github.com/b070nd/stoxicity/app/src/store"
)

// fakeIndex is an in-memory Index that holds the stored days of a test.
type fakeIndex map[string]store.IndexDay

func (f fakeIndex) IndexDays(_ context.Context, from string, to ...string) (map[string]store.IndexDay, error) {
	last := from
	if len(to) > 0 {
		last = to[0]
	}
	out := map[string]store.IndexDay{}
	for k, v := range f {
		if k >= from && k <= last {
			out[k] = v
		}
	}
	return out, nil
}

func TestMeasureReadsTheStoredDaysFromAnyIndex(t *testing.T) {
	idx := fakeIndex{
		"2026-11-09": {Day: "2026-11-09", Status: store.DayComplete, Basis: "daily", Forms: "8-K"},
		"2026-11-10": {Day: "2026-11-10", Status: store.DayError, Basis: "daily", LastError: "boom"},
	}
	c, err := Measure(context.Background(), idx, d("2026-11-09"), d("2026-11-11"), []string{"8-K"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Days != 3 || c.Complete != 1 || c.Failed != 1 || c.Unread != 1 || c.Resolved() != 1 {
		t.Fatalf("completeness = %+v", c)
	}
	if len(c.Gaps) != 2 || c.Gaps[0].Day != "2026-11-10" || c.Gaps[0].Why != "daily: boom" || c.Gaps[1].Day != "2026-11-11" || c.Gaps[1].Status != "unread" {
		t.Fatalf("gaps = %+v", c.Gaps)
	}
}
