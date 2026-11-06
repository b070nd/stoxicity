package status

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/scheduler"
)

var t0 = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

func src(id string, enabled bool) model.Source {
	return model.Source{ID: id, Enabled: enabled}
}

// The scheduler stores its levels in capitals; the readers had compared them
// with lower case, so every stored red source showed as having no colour.
func TestStoredLevelsAreMappedWhateverTheirCase(t *testing.T) {
	iv := 15 * time.Minute
	for name, tt := range map[string]struct {
		h    scheduler.Health
		want Level
	}{
		"never polled":  {scheduler.Health{}, Unknown},
		"worked":        {scheduler.Health{LastOK: t0.Add(-time.Minute), Level: scheduler.LevelGreen}, Green},
		"one failure":   {scheduler.Health{LastOK: t0.Add(-time.Minute), Failures: 1}, Yellow},
		"circuit open":  {scheduler.Health{LastOK: t0.Add(-time.Minute), Failures: 5, CircuitOpen: true}, Red},
		"never worked":  {scheduler.Health{Failures: 1}, Red},
		"stale success": {scheduler.Health{LastOK: t0.Add(-time.Hour), Level: scheduler.LevelGreen}, Yellow},
	} {
		if got := FromStored(src("a", true), tt.h, iv, t0).Level; got != tt.want {
			t.Errorf("%s: level %q, want %q", name, got, tt.want)
		}
	}
}

func TestStoredStates(t *testing.T) {
	iv := time.Minute
	if got := FromStored(src("a", false), scheduler.Health{}, iv, t0).State; got != Off {
		t.Errorf("disabled: %q", got)
	}
	if got := FromStored(src("a", true), scheduler.Health{Permanent: true, Failures: 1}, iv, t0).State; got != Parked {
		t.Errorf("permanent: %q", got)
	}
	due := t0.Add(time.Minute)
	s := FromStored(src("a", true), scheduler.Health{LastOK: t0, NextDue: due}, iv, t0)
	if s.State != Idle || !s.Until.Equal(due) {
		t.Errorf("idle: %+v", s)
	}
}

func TestSortPutsTroubleFirstAndDisabledLast(t *testing.T) {
	rows := []Source{
		{ID: "green", Enabled: true, Level: Green},
		{ID: "off", Enabled: false, State: Off, Level: Red},
		{ID: "new", Enabled: true},
		{ID: "red-b", Enabled: true, Level: Red},
		{ID: "yellow", Enabled: true, Level: Yellow},
		{ID: "red-a", Enabled: true, Level: Red},
	}
	Sort(rows)
	var got []string
	for _, r := range rows {
		got = append(got, r.ID)
	}
	want := []string{"red-a", "red-b", "yellow", "new", "green", "off"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

func TestBoardAdapterFoldsLateIntoItsRunningRow(t *testing.T) {
	b := scheduler.Board{Rows: []scheduler.Activity{
		{SourceID: "a", State: scheduler.Running, Since: t0, LastOK: t0, Level: scheduler.LevelGreen},
		{SourceID: "a", State: scheduler.Late, Since: t0.Add(time.Minute)},
		{SourceID: "b", State: scheduler.Resting, Host: "h", Until: t0.Add(time.Minute), Failures: 2, Level: scheduler.LevelYellow},
		{SourceID: "c", State: scheduler.Idle, Until: t0.Add(time.Hour), LastOK: t0, Level: scheduler.LevelGreen},
		{SourceID: "d", State: scheduler.Idle, Parked: true, Failures: 1, Level: scheduler.LevelRed},
	}}
	reg := map[string]model.Source{"c": {ID: "c", Enabled: false}}
	rows := FromBoard(b, reg)
	if len(rows) != 4 {
		t.Fatalf("rows %d, want 4 (late folded in)", len(rows))
	}
	if !rows[0].Late || rows[0].State != Running {
		t.Errorf("running row: %+v", rows[0])
	}
	if rows[1].State != Resting || rows[1].Level != Yellow {
		t.Errorf("resting row: %+v", rows[1])
	}
	if rows[2].Enabled {
		t.Errorf("registry should switch c off: %+v", rows[2])
	}
	if rows[3].State != Parked || rows[3].Level != Red {
		t.Errorf("parked row: %+v", rows[3])
	}
}

func TestMeters(t *testing.T) {
	rows := []Source{
		{ID: "a", Enabled: true, Level: Green, State: Running, Late: true},
		{ID: "b", Enabled: true, Level: Red, State: Parked},
		{ID: "c", Enabled: true, Level: Yellow, State: Idle, Until: t0.Add(time.Hour)},
		{ID: "d", Enabled: true, Level: Green, State: Idle, Until: t0.Add(time.Minute)},
		{ID: "e", Enabled: true, State: Waiting},
		{ID: "f", Enabled: false, State: Off},
	}
	m := Summarise(rows)
	want := Meters{
		Total: 5, Off: 1, Green: 2, Yellow: 1, Red: 1, Unknown: 1,
		Running: 1, Waiting: 1, Parked: 1, Late: 1, Next: "d", NextDue: t0.Add(time.Minute),
	}
	if m != want {
		t.Errorf("meters %+v\nwant   %+v", m, want)
	}
}
