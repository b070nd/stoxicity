package exchange

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The entries below are invented for the tests; none is a real announcement.
func testCalendar() Calendar {
	rec := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	return Calendar{
		Version: "test-1",
		Overrides: []Override{
			{Venue: "Nasdaq", Date: "2026-11-10", Kind: OverrideClosed, Reason: "invented closure", Source: "test://notice-1", Recorded: rec},
			{Venue: "Nasdaq", Date: "2026-11-11", Kind: OverrideEarly, Close: "11:00", Reason: "invented early close", Source: "test://notice-2", Recorded: rec},
			{Venue: "Nasdaq", Date: "2026-11-14", Kind: OverrideOpen, Close: "12:00", Reason: "invented Saturday session", Source: "test://notice-3", Recorded: rec},
		},
	}
}

func TestAClosureOverridesTheRecurringRules(t *testing.T) {
	n, c := nasdaq(t), testCalendar()
	if st := n.StatusAt(et(t, "2026-11-10 11:00")); !st.Open {
		t.Fatalf("recurring rules: %+v, an ordinary Tuesday", st)
	}
	st := c.StatusAt(n, et(t, "2026-11-10 11:00"))
	if st.Open || st.Closed != "invented closure" || st.Override == nil || st.Override.Source != "test://notice-1" {
		t.Errorf("override: %+v, want closed by the sourced override", st)
	}
	if got := c.SessionsOn(n, et(t, "2026-11-10 11:00")); len(got) != 0 {
		t.Errorf("sessions on a closure = %v", got)
	}
	if !strings.Contains(st.Caveat(), "test://notice-1") || !strings.Contains(st.Describe(et(t, "2026-11-10 11:00")), "scheduled closed") {
		t.Errorf("the status must name its source and say it is the schedule: %q / %q", st.Caveat(), st.Describe(et(t, "2026-11-10 11:00")))
	}
}

func TestAnEarlyOverrideMovesTheClose(t *testing.T) {
	n, c := nasdaq(t), testCalendar()
	if st := c.StatusAt(n, et(t, "2026-11-11 10:59")); !st.Open || !st.Core || !st.Early {
		t.Errorf("10:59: %+v, still the regular session", st)
	}
	if st := c.StatusAt(n, et(t, "2026-11-11 11:00")); !st.Open || st.Core || st.Session != "After-hours" {
		t.Errorf("11:00: %+v, the after-hours session starts at the overridden close", st)
	}
}

func TestAnOpenOverrideTradesOnAClosedDay(t *testing.T) {
	n, c := nasdaq(t), testCalendar()
	if st := n.StatusAt(et(t, "2026-11-14 10:00")); st.Open {
		t.Fatalf("recurring rules: %+v, Saturday", st)
	}
	if st := c.StatusAt(n, et(t, "2026-11-14 10:00")); !st.Open || st.Override == nil {
		t.Errorf("override: %+v, want a session on the special day", st)
	}
}

func TestAnOverrideOnlyAppliesToItsVenueAndDate(t *testing.T) {
	c := testCalendar()
	nyse, _ := Lookup("NYSE")
	if st := c.StatusAt(nyse, et(t, "2026-11-10 11:00")); !st.Open || st.Override != nil {
		t.Errorf("another venue: %+v", st)
	}
	if st := c.StatusAt(nasdaq(t), et(t, "2026-11-12 11:00")); !st.Open || st.Override != nil {
		t.Errorf("another day: %+v", st)
	}
}

func TestACalendarReadAsOfDoesNotHaveLaterOverrides(t *testing.T) {
	c := testCalendar().AsOf(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if st := c.StatusAt(nasdaq(t), et(t, "2026-11-10 11:00")); !st.Open {
		t.Errorf("the closure was recorded on 1 November: %+v", st)
	}
}

func TestAnUnsourcedOverrideIsRefused(t *testing.T) {
	c := Calendar{Version: "x", Overrides: []Override{{Venue: "Nasdaq", Date: "2026-11-10", Kind: OverrideClosed, Reason: "r"}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "source") {
		t.Errorf("Validate = %v, want a missing source and recorded time", err)
	}
	if st := c.StatusAt(nasdaq(t), et(t, "2026-11-10 11:00")); !st.Open || st.Override != nil {
		t.Errorf("an invalid override must not apply: %+v", st)
	}
	for _, o := range []Override{
		{Venue: "Nowhere", Date: "2026-11-10", Kind: OverrideClosed, Reason: "r", Source: "s", Recorded: time.Now()},
		{Venue: "Nasdaq", Date: "10 Nov", Kind: OverrideClosed, Reason: "r", Source: "s", Recorded: time.Now()},
		{Venue: "Nasdaq", Date: "2026-11-10", Kind: OverrideEarly, Reason: "r", Source: "s", Recorded: time.Now()},
		{Venue: "Nasdaq", Date: "2026-11-10", Kind: "maybe", Reason: "r", Source: "s", Recorded: time.Now()},
	} {
		if (Calendar{Version: "x", Overrides: []Override{o}}).Validate() == nil {
			t.Errorf("%+v passed validation", o)
		}
	}
}

func TestAnObservedHaltIsReportedBesideTheSchedule(t *testing.T) {
	n := nasdaq(t)
	now := et(t, "2026-11-05 14:00")
	c := Calendar{Version: "h", Observations: []Observation{{Venue: "Nasdaq", Kind: "halt", From: now.Add(-time.Hour), Source: "test://halt", Recorded: now.Add(-time.Hour)}}}
	st := c.StatusAt(n, now)
	if st.Halt == nil || !st.Open {
		t.Fatalf("%+v: the schedule stays open, the halt is reported beside it", st)
	}
	if !strings.HasPrefix(st.Describe(now), "halted (halt)") || !strings.Contains(st.Caveat(), "test://halt") {
		t.Errorf("describe %q caveat %q", st.Describe(now), st.Caveat())
	}
	if st := (Calendar{}).StatusAt(n, now); st.Halt != nil || !strings.Contains(st.Caveat(), "not observed") {
		t.Errorf("without observations the status must say halts are not observed: %q", st.Caveat())
	}
}

func TestLoadReadsTheFileAndRefusesABadOne(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(dir); err != nil || c.Version != "recurring-rules" || len(c.Overrides) != 0 {
		t.Fatalf("no file: %+v, %v", c, err)
	}
	good := "version: 2026-11-test\noverrides:\n  - venue: Nasdaq\n    date: \"2026-11-10\"\n    kind: closed\n    reason: invented\n    source: test://n\n    recorded: 2026-11-01T09:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil || c.Version != "2026-11-test" || len(c.Overrides) != 1 {
		t.Fatalf("good file: %+v, %v", c, err)
	}
	if st := c.StatusAt(nasdaq(t), et(t, "2026-11-10 11:00")); st.Open {
		t.Errorf("loaded override not applied: %+v", st)
	}
	bad := strings.Replace(good, "source: test://n\n", "", 1)
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("a file with an unsourced override must be refused whole")
	}
}
