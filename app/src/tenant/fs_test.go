package tenant

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tenant files written here are synthetic fixtures, not verified against live data.

// writeTenants creates files under a new tenants folder. The keys of files are
// slash-separated paths relative to that folder.
func writeTenants(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFSRegistryList(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string // IDs, in order
	}{
		{
			name: "sorted by ID; folders without tenant.yaml, invalid names and loose files are skipped",
			files: map[string]string{
				"beta/tenant.yaml":     "id: beta\nname: Beta\n",
				"alpha/tenant.yaml":    "id: alpha\nname: Alpha\ndefault_watch: all\n",
				"empty/notes.txt":      "no tenant file here",
				"Bad_Name/tenant.yaml": "id: Bad_Name\n",
				"loose.yaml":           "id: loose\n",
			},
			want: []string{"alpha", "beta"},
		},
		{
			name:  "no tenants at all",
			files: map[string]string{"README.txt": "nothing"},
			want:  nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeTenants(t, c.files)
			got, err := FSRegistry{Dir: root}.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %d tenants, want %d: %+v", len(got), len(c.want), got)
			}
			for i, id := range c.want {
				if got[i].ID != id {
					t.Errorf("tenant %d = %q, want %q", i, got[i].ID, id)
				}
				if got[i].Dir != filepath.Join(root, id) {
					t.Errorf("tenant %q Dir = %q", id, got[i].Dir)
				}
			}
		})
	}
}

func TestFSRegistryListErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "tenant.yaml names another ID than its folder",
			files: map[string]string{"gamma/tenant.yaml": "id: delta\n"},
			want:  "must equal the folder name",
		},
		{
			name:  "unknown key in tenant.yaml",
			files: map[string]string{"x/tenant.yaml": "id: x\ncolour: red\n"},
			want:  "colour",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := FSRegistry{Dir: writeTenants(t, c.files)}.List()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, c.want)
			}
		})
	}
}

func TestFSRegistryGet(t *testing.T) {
	// x/ sits beside tenants/, so "../x" would reach it if the ID were not checked.
	base := writeTenants(t, map[string]string{
		"tenants/default/tenant.yaml": "id: default\nname: Default\ndefault_watch: all-relevant\n",
		"x/tenant.yaml":               "id: x\n",
	})
	root := filepath.Join(base, "tenants")
	reg := FSRegistry{Dir: root}

	t.Run("found", func(t *testing.T) {
		got, err := reg.Get("default")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "default" || got.Name != "Default" || got.DefaultWatch != "all-relevant" ||
			got.Dir != filepath.Join(root, "default") {
			t.Errorf("Get(default) = %+v", got)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := reg.Get("nope")
		if err == nil || err.Error() != `tenant: unknown tenant "nope"` {
			t.Errorf("error = %v", err)
		}
	})

	invalid := []string{"../x", "x/../x", "a/b", "", "..", "Default", "has space"}
	for _, id := range invalid {
		t.Run("rejects "+id, func(t *testing.T) {
			_, err := reg.Get(id)
			if err == nil || !strings.HasPrefix(err.Error(), "tenant: invalid id") {
				t.Errorf("Get(%q) error = %v, want an invalid-id error", id, err)
			}
		})
	}
}

func TestFSRegistryRealTenants(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	reg := FSRegistry{Dir: filepath.Join(filepath.Dir(file), "..", "..", "config", "tenants")}
	list, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tn := range list {
		if tn.ID == Default {
			found = true
		}
	}
	if !found {
		t.Fatalf("shipped tenants %+v do not include %q", list, Default)
	}
	if _, err := reg.Get(Default); err != nil {
		t.Fatal(err)
	}
}
