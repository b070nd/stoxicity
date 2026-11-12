package research

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/store"
)

// memRecords is an in-memory Records with the semantics of the store: it is
// append-only, a record is identified by kind, key and content, and appending
// content that is already held leaves it where it is.
type memRecords struct{ recs []store.Record }

func (m *memRecords) Append(_ context.Context, in ...store.RecordInput) ([]store.Record, int, error) {
	var out []store.Record
	fresh := 0
	for _, x := range in {
		hash := store.RecordHash(x.Body)
		found := false
		for _, r := range m.recs {
			if r.Kind == x.Kind && r.Key == x.Key && r.Hash == hash {
				out = append(out, r)
				found = true
				break
			}
		}
		if found {
			continue
		}
		r := store.Record{Seq: int64(len(m.recs) + 1), Kind: x.Kind, Key: x.Key, Hash: hash, Body: append([]byte(nil), x.Body...)}
		m.recs = append(m.recs, r)
		out = append(out, r)
		fresh++
	}
	return out, fresh, nil
}

func (m *memRecords) History(_ context.Context, kind, key string) ([]store.Record, error) {
	var out []store.Record
	for _, r := range m.recs {
		if r.Kind == kind && r.Key == key {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memRecords) Latest(ctx context.Context, kind, key string) (store.Record, error) {
	h, _ := m.History(ctx, kind, key)
	if len(h) == 0 {
		return store.Record{}, store.ErrNoRecord
	}
	return h[len(h)-1], nil
}

func (m *memRecords) Current(_ context.Context, kind, prefix string) ([]store.Record, error) {
	newest := map[string]store.Record{}
	for _, r := range m.recs {
		if r.Kind == kind && strings.HasPrefix(r.Key, prefix) {
			newest[r.Key] = r
		}
	}
	var out []store.Record
	for _, r := range newest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *memRecords) All(_ context.Context, kind, prefix string) ([]store.Record, error) {
	var out []store.Record
	for _, r := range m.recs {
		if r.Kind == kind && strings.HasPrefix(r.Key, prefix) {
			out = append(out, r)
		}
	}
	return out, nil
}

func labSpec(id string, horizon int) eventstudy.Spec {
	s := eventstudy.DefaultSpec()
	s.ID = id
	s.Types = []string{"insider_buy"}
	s.Benchmark = "SPY"
	s.Horizons = []int{horizon}
	s.Development = eventstudy.Window{From: "2023-01-01", To: "2024-12-31"}
	s.Validation = eventstudy.Window{From: "2025-01-01", To: "2025-12-31"}
	return s
}

func labPlaybook(id, study string, sessions int) playbook.Playbook {
	return playbook.Playbook{ID: id, Study: study, Rules: []playbook.Rule{{
		ID: "buy-insider-buys", Types: []string{"insider_buy"}, Action: playbook.Buy,
		Evidence: playbook.Evidence{Sessions: sessions, MinEvents: 50, Direction: "up", MinEdge: 0.01},
	}}}
}

func TestLabRegisterStudy(t *testing.T) {
	steps := []struct {
		name    string
		spec    eventstudy.Spec
		want    Registration
		errPart string
	}{
		{"new", labSpec("a", 5), Registration{Revision: 1}, ""},
		{"unchanged", labSpec("a", 5), Registration{Revision: 1, Existed: true, Unchanged: true}, ""},
		{"new revision", labSpec("a", 10), Registration{Revision: 2, Existed: true}, ""},
		{"old content again", labSpec("a", 5), Registration{}, "cannot be made the newest revision again"},
		{"another id", labSpec("b", 5), Registration{Revision: 1}, ""},
		{"invalid", eventstudy.Spec{ID: "c"}, Registration{}, "names no event type"},
	}
	lab := Lab{Records: &memRecords{}}
	ctx := context.Background()
	for _, s := range steps {
		got, err := lab.RegisterStudy(ctx, s.spec)
		switch {
		case s.errPart != "":
			if err == nil || !strings.Contains(err.Error(), s.errPart) {
				t.Fatalf("%s: error = %v, want one with %q", s.name, err, s.errPart)
			}
		case err != nil:
			t.Fatalf("%s: %v", s.name, err)
		case got != s.want:
			t.Errorf("%s: registration = %+v, want %+v", s.name, got, s.want)
		}
	}
	studies, revisions, err := lab.StudyCounts(ctx)
	if err != nil || studies != 2 || revisions != 3 {
		t.Errorf("counts = %d studies, %d revisions, %v; want 2 and 3", studies, revisions, err)
	}
}

func TestLabStoredStudyBodyIsTheSpecJSON(t *testing.T) {
	m := &memRecords{}
	lab := Lab{Records: m}
	spec := labSpec("a", 5)
	if _, err := lab.RegisterStudy(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(spec)
	if len(m.recs) != 1 || m.recs[0].Kind != "study.spec" || !bytes.Equal(m.recs[0].Body, want) {
		t.Errorf("stored %+v, want one study.spec record holding %s", m.recs, want)
	}
}

func TestLabLatestAndListings(t *testing.T) {
	ctx := context.Background()
	lab := Lab{Records: &memRecords{}}
	if _, _, err := lab.LatestStudy(ctx, "a"); err == nil || !strings.Contains(err.Error(), "no study a: register it first (study register -file F)") {
		t.Fatalf("LatestStudy of nothing: %v", err)
	}
	if _, _, err := lab.LatestPlaybook(ctx, "p"); err == nil || !strings.Contains(err.Error(), "no playbook p: register it first (playbook register -file F)") {
		t.Fatalf("LatestPlaybook of nothing: %v", err)
	}
	if ok, err := lab.HasStudy(ctx, "a"); err != nil || ok {
		t.Fatalf("HasStudy before registering = %v, %v", ok, err)
	}
	for _, h := range []int{5, 10} {
		if _, err := lab.RegisterStudy(ctx, labSpec("a", h)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lab.RegisterStudy(ctx, labSpec("b", 5)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []int{20, 30} {
		if _, err := lab.RegisterPlaybook(ctx, labPlaybook("p", "a", s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := lab.RecordRun(ctx, "a", Run{Run: 1, Report: eventstudy.Report{Status: eventstudy.Insufficient}}); err != nil {
		t.Fatal(err)
	}

	spec, rev, err := lab.LatestStudy(ctx, "a")
	if err != nil || rev != 2 || len(spec.Horizons) != 1 || spec.Horizons[0] != 10 {
		t.Errorf("LatestStudy = %+v, %d, %v; want horizon 10 at revision 2", spec.Horizons, rev, err)
	}
	p, rev, err := lab.LatestPlaybook(ctx, "p")
	if err != nil || rev != 2 || p.Rules[0].Evidence.Sessions != 30 {
		t.Errorf("LatestPlaybook = sessions %d, revision %d, %v; want 30 at 2", p.Rules[0].Evidence.Sessions, rev, err)
	}
	if ok, err := lab.HasStudy(ctx, "a"); err != nil || !ok {
		t.Errorf("HasStudy after registering = %v, %v", ok, err)
	}

	studies, err := lab.Studies(ctx)
	if err != nil || len(studies) != 2 {
		t.Fatalf("Studies = %d, %v; want 2", len(studies), err)
	}
	if a := studies[0]; a.Spec.ID != "a" || a.Revisions != 2 || a.Runs != 1 || a.LastStatus != eventstudy.Insufficient {
		t.Errorf("study a summary = %+v", a)
	}
	if b := studies[1]; b.Spec.ID != "b" || b.Revisions != 1 || b.Runs != 0 || b.LastStatus != "" {
		t.Errorf("study b summary = %+v", b)
	}
	pbs, err := lab.Playbooks(ctx)
	if err != nil || len(pbs) != 1 || pbs[0].Playbook.ID != "p" || pbs[0].Revisions != 2 {
		t.Errorf("Playbooks = %+v, %v", pbs, err)
	}
}

func TestLabRegisterPlaybook(t *testing.T) {
	steps := []struct {
		name string
		p    playbook.Playbook
		want Registration
	}{
		{"new", labPlaybook("p", "a", 20), Registration{Revision: 1}},
		{"unchanged", labPlaybook("p", "a", 20), Registration{Revision: 1, Existed: true, Unchanged: true}},
		{"new revision", labPlaybook("p", "a", 30), Registration{Revision: 2, Existed: true}},
	}
	lab := Lab{Records: &memRecords{}}
	for _, s := range steps {
		got, err := lab.RegisterPlaybook(context.Background(), s.p)
		if err != nil || got != s.want {
			t.Errorf("%s: registration = %+v, %v; want %+v", s.name, got, err, s.want)
		}
	}
	_, err := lab.RegisterPlaybook(context.Background(), labPlaybook("p", "a", 20))
	if err == nil || !strings.Contains(err.Error(), "cannot be made the newest revision again") {
		t.Errorf("registering old rules again: %v", err)
	}
	if _, err := lab.RegisterPlaybook(context.Background(), playbook.Playbook{ID: "q"}); err == nil {
		t.Error("an invalid playbook was registered")
	}
}

func TestLabRuns(t *testing.T) {
	ctx := context.Background()
	lab := Lab{Records: &memRecords{}}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	if runs, err := lab.Runs(ctx, "a"); err != nil || len(runs) != 0 {
		t.Fatalf("runs before any = %v, %v", runs, err)
	}
	for i := 1; i <= 2; i++ {
		run := Run{At: at.Add(time.Duration(i) * time.Hour), Run: i, SpecHash: "s", DataHash: "d", Report: eventstudy.Report{Status: eventstudy.Insufficient}}
		if err := lab.RecordRun(ctx, "a", run); err != nil {
			t.Fatal(err)
		}
	}
	if err := lab.RecordRun(ctx, "other", Run{Run: 1}); err != nil {
		t.Fatal(err)
	}
	runs, err := lab.Runs(ctx, "a")
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %d, %v; want 2", len(runs), err)
	}
	if runs[0].Run != 1 || runs[1].Run != 2 || !runs[1].At.Equal(at.Add(2*time.Hour)) || runs[1].Report.Status != eventstudy.Insufficient {
		t.Errorf("runs = %+v, want oldest first with their content", runs)
	}
}

func TestLabValidationReads(t *testing.T) {
	read := eventstudy.Report{Spec: eventstudy.Spec{Validation: eventstudy.Window{From: "2025-01-01", To: "2025-12-31"}}}
	unread := eventstudy.Report{}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name  string
		runs  []Run
		reads int
		first time.Time
	}{
		{"no runs", nil, 0, time.Time{}},
		{"only development", []Run{{At: day(1), Report: unread}}, 0, time.Time{}},
		{"first read is the first counted", []Run{{At: day(1), Report: unread}, {At: day(2), Report: read}, {At: day(3), Report: read}}, 2, day(2)},
		{"one end of the window is enough", []Run{{At: day(4), Report: eventstudy.Report{Spec: eventstudy.Spec{Validation: eventstudy.Window{To: "2025-12-31"}}}}}, 1, day(4)},
	}
	for _, tc := range tests {
		reads, first := ValidationReads(tc.runs)
		if reads != tc.reads || !first.Equal(tc.first) {
			t.Errorf("%s: reads = %d first %v, want %d first %v", tc.name, reads, first, tc.reads, tc.first)
		}
	}
}

func TestLabSavedSignals(t *testing.T) {
	ctx := context.Background()
	lab := Lab{Records: &memRecords{}}
	mk := func(id, pb string) SavedSignal {
		return SavedSignal{Signal: playbook.Signal{ID: id, Playbook: pb}, Decision: playbook.Decision{By: "gate", Reasons: []string{id}}}
	}
	if fresh, err := lab.SaveSignals(ctx, nil); err != nil || fresh != 0 {
		t.Fatalf("saving nothing = %d, %v", fresh, err)
	}
	if fresh, err := lab.SaveSignals(ctx, []SavedSignal{mk("s1", "p"), mk("s2", "q")}); err != nil || fresh != 2 {
		t.Fatalf("first save = %d, %v; want 2", fresh, err)
	}
	if fresh, err := lab.SaveSignals(ctx, []SavedSignal{mk("s2", "q"), mk("s3", "p")}); err != nil || fresh != 1 {
		t.Fatalf("second save = %d, %v; want 1 (s2 was saved before)", fresh, err)
	}
	tests := []struct {
		name, playbook string
		want           []string
	}{
		{"all, newest first", "", []string{"s3", "s2", "s1"}},
		{"one playbook", "p", []string{"s3", "s1"}},
		{"unknown playbook", "zzz", nil},
	}
	for _, tc := range tests {
		got, err := lab.SavedSignals(ctx, tc.playbook)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var ids []string
		for _, x := range got {
			ids = append(ids, x.Signal.ID)
			if len(x.Decision.Reasons) != 1 || x.Decision.Reasons[0] != x.Signal.ID {
				t.Errorf("%s: decision of %s = %+v", tc.name, x.Signal.ID, x.Decision)
			}
		}
		if strings.Join(ids, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: ids = %v, want %v", tc.name, ids, tc.want)
		}
	}
}
