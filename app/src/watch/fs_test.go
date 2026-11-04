package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/dashboard"
	"github.com/b070nd/stoxicity/app/src/model"
)

// The watch files written here are synthetic fixtures, not verified against live data.

// fullWatch sets every field, so a round trip through YAML returns it unchanged.
func fullWatch(id string) Watch {
	return Watch{
		ID: id, Name: "Full watch", Description: "Every field set.",
		Scope: Scope{
			Tickers: []string{"KO"}, Topics: []string{"ai-infra"}, Sectors: []string{"consumer"},
			Domains: []string{"sec.gov"}, ExcludeDomains: []string{"spam.example"},
			Groups: []model.EventGroup{model.GroupResults}, Kinds: []model.EventKind{model.EventDividend},
		},
		Query: `earnings -"price target"`,
		Lens:  Lens{Threshold: 0.6, MinMateriality: 3},
		Alerts: []Alert{{
			Name: "material", When: When{MinRelevance: 0.7, MinMateriality: 4, Kinds: []model.EventKind{model.EventBuyback}},
			Channels: []channel.Kind{channel.TUI, channel.Email}, Quiet: "22:00-07:00", Cooldown: "2h",
		}},
		Digest:    Digest{Cadence: "daily", At: "07:30", Channels: []channel.Kind{channel.File}},
		Dashboard: []dashboard.Widget{dashboard.Alerts, dashboard.Digest},
	}
}

// writeWatches creates files in a new watches folder. The keys of files are
// names relative to that folder.
func writeWatches(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// watchesDir returns the shipped tenants/default/watches folder.
func watchesDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "config", "tenants", "default", "watches")
}

func TestFSStoreRoundTrip(t *testing.T) {
	s := FSStore{Dir: filepath.Join(t.TempDir(), "watches")}
	want := fullWatch("buffett-core")
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("buffett-core")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the watch:\ngot  %+v\nwant %+v", got, want)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !reflect.DeepEqual(list[0], want) {
		t.Errorf("List = %+v", list)
	}
}

func TestFSStoreSaveCreatesFolderAndModeAndReplaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "watches")
	s := FSStore{Dir: dir}
	w := fullWatch("macro-radar")
	if err := s.Save(w); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "macro-radar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %o, want 644", perm)
	}
	w.Name = "Renamed"
	if err := s.Save(w); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("macro-radar")
	if err != nil || got.Name != "Renamed" {
		t.Fatalf("after overwrite: %+v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("folder holds %d entries after a save, want only the watch file", len(entries))
	}
}

func TestFSStoreSaveRejectsInvalid(t *testing.T) {
	badThreshold := fullWatch("ok-id")
	badThreshold.Lens.Threshold = 2
	cases := []struct {
		name string
		w    Watch
	}{
		{"path traversal id", fullWatch("../x")},
		{"upper-case id", fullWatch("Buffett")},
		{"empty name", func() Watch { w := fullWatch("ok-id"); w.Name = " "; return w }()},
		{"threshold out of range", badThreshold},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "watches")
			if err := (FSStore{Dir: dir}).Save(c.w); err == nil {
				t.Fatal("expected an error")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("a rejected save left the folder behind: %v", err)
			}
		})
	}
}

func TestFSStoreInvalidIDsNeverTouchFiles(t *testing.T) {
	// A file one level above the watches folder, reachable only by traversal.
	root := t.TempDir()
	dir := filepath.Join(root, "watches")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "x.yaml")
	if err := os.WriteFile(sentinel, []byte("id: x\nname: X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := FSStore{Dir: dir}
	for _, id := range []string{"../x", "a/b", "", "..", "Bad Id", "x.yaml"} {
		t.Run(id, func(t *testing.T) {
			if _, err := s.Get(id); err == nil || !strings.HasPrefix(err.Error(), "watch: invalid id") {
				t.Errorf("Get(%q) error = %v", id, err)
			}
			if err := s.Delete(id); err == nil || !strings.HasPrefix(err.Error(), "watch: invalid id") {
				t.Errorf("Delete(%q) error = %v", id, err)
			}
		})
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("the sentinel outside the folder was touched: %v", err)
	}
}

func TestFSStoreGetAndDeleteUnknown(t *testing.T) {
	s := FSStore{Dir: writeWatches(t, nil)}
	if _, err := s.Get("nope"); err == nil || err.Error() != `watch: unknown watch "nope"` {
		t.Errorf("Get error = %v", err)
	}
	if err := s.Delete("nope"); err == nil || err.Error() != `watch: unknown watch "nope"` {
		t.Errorf("Delete error = %v", err)
	}
}

func TestFSStoreDelete(t *testing.T) {
	s := FSStore{Dir: t.TempDir()}
	if err := s.Save(fullWatch("discovery")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("discovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("discovery"); err == nil {
		t.Error("the deleted watch can still be read")
	}
	if err := s.Delete("discovery"); err == nil {
		t.Error("deleting twice should fail")
	}
}

func TestFSStoreListFiltersAndSorts(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "only exact .yaml files, sorted by watch ID",
			files: map[string]string{
				"buffett-core.yaml":         "id: buffett-core\nname: Core\n",
				"buffett.yaml":              "id: buffett\nname: Plain\n",
				"buffett-core.yaml.example": "not: [valid",
				"notes.txt":                 "not: [valid",
				"old.yml":                   "not: [valid",
			},
			want: []string{"buffett", "buffett-core"},
		},
		{
			name:  "a folder named like a watch is ignored",
			files: map[string]string{"sub.yaml/inner.txt": "x"},
			want:  nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeWatches(t, nil)
			for name, body := range c.files {
				if strings.Contains(name, "/") {
					if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := FSStore{Dir: dir}.List()
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, w := range got {
				ids = append(ids, w.ID)
			}
			if !reflect.DeepEqual(ids, c.want) {
				t.Errorf("IDs = %v, want %v", ids, c.want)
			}
		})
	}
}

func TestFSStoreListMissingFolderIsEmpty(t *testing.T) {
	got, err := FSStore{Dir: filepath.Join(t.TempDir(), "absent")}.List()
	if err != nil || len(got) != 0 {
		t.Errorf("List = %v, %v; want no watches and no error", got, err)
	}
}

func TestFSStoreListReportsEveryProblem(t *testing.T) {
	dir := writeWatches(t, map[string]string{
		"alpha.yaml": "id: beta\nname: Mismatched\n",
		"gamma.yaml": "id: gamma\nname: Gamma\nlens:\n  threshold: 2\n",
		"delta.yaml": "id: delta\nname: Delta\ncolour: red\n",
		"fine.yaml":  "id: fine\nname: Fine\n",
	})
	_, err := FSStore{Dir: dir}.List()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"alpha.yaml", "gamma.yaml", "threshold", "delta.yaml", "colour"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "fine.yaml") {
		t.Errorf("a valid file is reported:\n%v", err)
	}
}

func TestFSStoreShippedAllRelevant(t *testing.T) {
	got, err := FSStore{Dir: watchesDir(t)}.Get("all-relevant")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Everything relevant" || len(got.Dashboard) != 6 {
		t.Errorf("all-relevant = %+v", got)
	}
}

func TestFSStoreShippedExamplesLoad(t *testing.T) {
	examples, err := filepath.Glob(filepath.Join(watchesDir(t), "*.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 {
		t.Fatal("no *.yaml.example files found; the check would pass vacuously")
	}
	for _, path := range examples {
		name := strings.TrimSuffix(filepath.Base(path), ".example")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := FSStore{Dir: dir}.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID+ext != name {
				t.Errorf("loaded %+v from %s", got, name)
			}
		})
	}
}
