package exchange

import (
	"strings"
	"testing"
	"time"
)

func et(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, mustNY(t))
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func mustNY(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func nasdaq(t *testing.T) Info {
	t.Helper()
	i, ok := Lookup("nasdaq")
	if !ok {
		t.Fatal("Nasdaq is not known")
	}
	return i
}

func TestLookupIgnoresCaseAndKnowsAliases(t *testing.T) {
	for in, want := range map[string]string{"NYSE": "NYSE", "nyse arca": "NYSE Arca", "CBOE": "Cboe BZX", " bats ": "Cboe BZX", "Nasdaq Stock Market": "Nasdaq"} {
		if i, ok := Lookup(in); !ok || i.Name != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", in, i.Name, ok, want)
		}
	}
	if _, ok := Lookup("Tokyo"); ok {
		t.Error("an exchange there is no reference data for must not be found")
	}
}

func TestEveryExchangeHasWhatTheDetailScreenShows(t *testing.T) {
	for _, i := range Known() {
		if i.Full == "" || i.Operator == "" || i.About == "" || i.Lists == "" || len(i.Sessions) == 0 {
			t.Errorf("%s: incomplete: %+v", i.Name, i)
		}
		if _, err := time.LoadLocation(i.Zone); err != nil {
			t.Errorf("%s: zone %q: %v", i.Name, i.Zone, err)
		}
		core := 0
		for _, s := range i.Sessions {
			if s.Core {
				core++
			}
		}
		if core != 1 {
			t.Errorf("%s has %d regular sessions, want 1", i.Name, core)
		}
	}
}

func TestHolidays2026(t *testing.T) {
	loc := mustNY(t)
	want := map[string]string{
		"2026-01-01": "New Year's Day", "2026-01-19": "Martin Luther King Jr. Day", "2026-02-16": "Washington's Birthday",
		"2026-04-03": "Good Friday", "2026-05-25": "Memorial Day", "2026-06-19": "Juneteenth",
		"2026-07-03": "Independence Day", // the 4th is a Saturday
		"2026-09-07": "Labor Day", "2026-11-26": "Thanksgiving Day", "2026-12-25": "Christmas Day",
	}
	got := map[string]string{}
	for _, h := range holidaysIn(2026, loc) {
		got[h.Date.Format("2006-01-02")] = h.Name
	}
	for d, n := range want {
		if got[d] != n {
			t.Errorf("%s: %q, want %q", d, got[d], n)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d holidays, want %d: %v", len(got), len(want), got)
	}
}

func TestHolidayObservance(t *testing.T) {
	loc := mustNY(t)
	has := func(year int, date string) bool {
		for _, h := range holidaysIn(year, loc) {
			if h.Date.Format("2006-01-02") == date {
				return true
			}
		}
		return false
	}
	if has(2022, "2021-12-31") || has(2022, "2022-01-01") || has(2022, "2022-01-03") {
		t.Error("New Year's Day on a Saturday is not observed, and 2022 has no January holiday on the 3rd")
	}
	if !has(2023, "2023-01-02") {
		t.Error("New Year's Day on a Sunday is observed on the Monday")
	}
	if !has(2025, "2025-04-18") || !has(2024, "2024-03-29") {
		t.Error("Good Friday: 2025-04-18 and 2024-03-29")
	}
	if has(2021, "2021-06-18") {
		t.Error("Juneteenth was not a market holiday before 2022")
	}
}

func TestEarlyCloses(t *testing.T) {
	n := nasdaq(t)
	for date, early := range map[string]bool{"2026-11-27": true, "2026-12-24": true, "2025-07-03": true, "2026-11-30": false, "2026-07-03": false} {
		d := n.DayOf(et(t, date+" 12:00"))
		if d.Early != early {
			t.Errorf("%s early = %v (%+v), want %v", date, d.Early, d, early)
		}
	}
	if d := n.DayOf(et(t, "2026-07-03 12:00")); d.Closed != "Independence Day" {
		t.Errorf("2026-07-03: %+v, the holiday is observed on the Friday", d)
	}
}

func TestStatusThroughADay(t *testing.T) {
	n := nasdaq(t)
	cases := []struct {
		at      string
		open    bool
		session string
		core    bool
	}{
		{"2026-11-05 03:59", false, "Pre-market", false},
		{"2026-11-05 04:00", true, "Pre-market", false},
		{"2026-11-05 09:30", true, "Regular", true},
		{"2026-11-05 15:59", true, "Regular", true},
		{"2026-11-05 16:00", true, "After-hours", false},
		{"2026-11-05 20:00", false, "Pre-market", false},
		{"2026-11-07 11:00", false, "Pre-market", false}, // a Saturday
	}
	for _, c := range cases {
		st := n.StatusAt(et(t, c.at))
		if st.Open != c.open || st.Session != c.session || st.Core != c.core {
			t.Errorf("%s: %+v, want open=%v %s core=%v", c.at, st, c.open, c.session, c.core)
		}
	}
	sat := n.StatusAt(et(t, "2026-11-07 11:00"))
	if sat.Closed != "weekend" || !sat.Change.Equal(et(t, "2026-11-09 04:00")) {
		t.Errorf("Saturday: %+v, want closed for the weekend until Monday 04:00", sat)
	}
}

func TestEarlyCloseTrimsTheSessions(t *testing.T) {
	n := nasdaq(t)
	if st := n.StatusAt(et(t, "2026-11-27 12:59")); !st.Open || !st.Core || !st.Early {
		t.Errorf("12:59: %+v, still the regular session", st)
	}
	if st := n.StatusAt(et(t, "2026-11-27 13:00")); !st.Open || st.Core || st.Session != "After-hours" {
		t.Errorf("13:00: %+v, the after-hours session starts at the early close", st)
	}
	if st := n.StatusAt(et(t, "2026-11-27 17:00")); st.Open {
		t.Errorf("17:00: %+v, after-hours ends at 17:00 on an early-close day", st)
	}
}

func TestStatusDescribe(t *testing.T) {
	n := nasdaq(t)
	now := et(t, "2026-11-05 14:00")
	if got := n.StatusAt(now).Describe(now); got != "scheduled open · Regular, ends in 2h00m" {
		t.Errorf("open: %q", got)
	}
	now = et(t, "2026-11-07 11:00")
	if got := n.StatusAt(now).Describe(now); !strings.HasPrefix(got, "scheduled closed (weekend) · Pre-market opens in 1d17h") {
		t.Errorf("closed: %q", got)
	}
}

func TestUpcomingHolidays(t *testing.T) {
	n := nasdaq(t)
	got := n.Holidays(et(t, "2026-11-06 10:00"), 3)
	if len(got) != 3 || got[0].Name != "Thanksgiving Day" || got[1].Name != "Christmas Day" || got[2].Name != "New Year's Day" || got[2].Date.Year() != 2027 {
		t.Errorf("holidays = %+v", got)
	}
}

func TestEasterAgainstKnownDates(t *testing.T) {
	loc := time.UTC
	for year, want := range map[int]string{2024: "2024-03-31", 2025: "2025-04-20", 2026: "2026-04-05", 2038: "2038-04-25"} {
		if got := easter(year, loc).Format("2006-01-02"); got != want {
			t.Errorf("easter(%d) = %s, want %s", year, got, want)
		}
	}
}
