package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shippedYAML is a synthetic sources.yaml for these tests. It is not verified
// against live data.
const shippedYAML = `sources:
  - id: sec-edgar
    name: SEC EDGAR, current filings
    domain: sec.gov
    kind: filings
    url: https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&output=atom
    tier: 1
    tags: [filings, us]
    forms: ["8-K", "10-K"]
    licence: redistributable
    terms: https://www.sec.gov/about/privacy-information
    paged: true
    enabled: false
  - id: fed-press
    name: Federal Reserve, press releases
    domain: federalreserve.gov
    kind: rss
    url: https://www.federalreserve.gov/feeds/press_all.xml
    tier: 1
    licence: internal
    owner: default
    enabled: true
`

// writeConfig creates a configuration folder in a temporary directory. The
// keys of files are slash-separated paths relative to that folder.
func writeConfig(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadOverrides(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		check func(*testing.T, Registry)
	}{
		{
			name:  "no sources folder, shipped list only",
			files: map[string]string{"sources.yaml": shippedYAML},
			check: func(t *testing.T, r Registry) {
				if len(r) != 2 || r["sec-edgar"].Enabled || !r["fed-press"].Enabled {
					t.Fatalf("registry = %+v", r)
				}
			},
		},
		{
			name: "override that names only enabled keeps licence, terms and URL",
			files: map[string]string{
				"sources.yaml":               shippedYAML,
				"sources/on.yaml":            "sources:\n  - id: sec-edgar\n    enabled: true\n",
				"sources/notes.yaml.example": "not: [valid", // not a .yaml file, so it must not be read
			},
			check: func(t *testing.T, r Registry) {
				s := r["sec-edgar"]
				if !s.Enabled {
					t.Error("enabled was not set")
				}
				if s.URL != "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&output=atom" ||
					s.Licence != "redistributable" || s.Terms != "https://www.sec.gov/about/privacy-information" {
					t.Errorf("unnamed fields changed: %+v", s)
				}
				if s.Name != "SEC EDGAR, current filings" || len(s.Forms) != 2 || !s.Paged || s.Owner != "" {
					t.Errorf("unnamed fields changed: %+v", s)
				}
			},
		},
		{
			name: "override that names enabled: false switches a source off",
			files: map[string]string{
				"sources.yaml":     shippedYAML,
				"sources/off.yaml": "sources:\n  - id: fed-press\n    enabled: false\n",
			},
			check: func(t *testing.T, r Registry) {
				s := r["fed-press"]
				if s.Enabled || s.Owner != "default" || s.Licence != "internal" {
					t.Errorf("fed-press = %+v", s)
				}
			},
		},
		{
			name: "entry with a new id is added whole",
			files: map[string]string{
				"sources.yaml": shippedYAML,
				"sources/ecb.yaml": `sources:
  - id: ecb-press
    name: ECB press releases
    domain: ecb.europa.eu
    kind: rss
    url: https://www.ecb.europa.eu/rss/press.html
    tier: 1
    licence: redistributable
    terms: https://www.ecb.europa.eu/home/disclaimer/html/index.en.html
    enabled: true
`,
			},
			check: func(t *testing.T, r Registry) {
				if len(r) != 3 {
					t.Fatalf("want 3 sources, got %d", len(r))
				}
				s := r["ecb-press"]
				if s.ID != "ecb-press" || s.Domain != "ecb.europa.eu" || !s.Enabled || s.Terms == "" {
					t.Errorf("ecb-press = %+v", s)
				}
			},
		},
		{
			name: "later file overrides earlier one, and keeps fields neither names",
			files: map[string]string{
				"sources.yaml":   shippedYAML,
				"sources/a.yaml": "sources:\n  - id: sec-edgar\n    name: First\n    enabled: true\n",
				"sources/b.yaml": "sources:\n  - id: sec-edgar\n    name: Second\n",
			},
			check: func(t *testing.T, r Registry) {
				s := r["sec-edgar"]
				if s.Name != "Second" || !s.Enabled {
					t.Errorf("sec-edgar = %+v; want name Second and enabled from the earlier file", s)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg, err := Load(writeConfig(t, c.files))
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, reg)
		})
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "missing sources.yaml",
			files: map[string]string{"sources/x.yaml": shippedYAML},
			want:  "sources.yaml",
		},
		{
			name: "duplicate id in one override file",
			files: map[string]string{
				"sources.yaml":     shippedYAML,
				"sources/dup.yaml": "sources:\n  - id: sec-edgar\n    enabled: true\n  - id: sec-edgar\n    enabled: false\n",
			},
			want: "appears twice in one file",
		},
		{
			name: "duplicate id in sources.yaml",
			files: map[string]string{
				"sources.yaml": shippedYAML + "  - id: fed-press\n    name: again\n    kind: rss\n    url: https://x.example.com\n    tier: 1\n    licence: internal\n    owner: default\n",
			},
			want: "appears twice in one file",
		},
		{
			name: "unknown field in an override",
			files: map[string]string{
				"sources.yaml":      shippedYAML,
				"sources/typo.yaml": "sources:\n  - id: sec-edgar\n    enabld: true\n",
			},
			want: "field enabld not found",
		},
		{
			name: "unknown top-level key in sources.yaml",
			files: map[string]string{
				"sources.yaml": "srcs:\n  - id: x\n",
			},
			want: "field srcs not found",
		},
		{
			name: "override makes a shipped source invalid, error names the file",
			files: map[string]string{
				"sources.yaml":     shippedYAML,
				"sources/bad.yaml": "sources:\n  - id: sec-edgar\n    url: http://insecure.example.com/feed\n",
			},
			want: "sources/bad.yaml",
		},
		{
			name: "new source fails Check",
			files: map[string]string{
				"sources.yaml":     shippedYAML,
				"sources/new.yaml": "sources:\n  - id: new-one\n    kind: rss\n    url: https://x.example.com\n    tier: 1\n    licence: redistributable\n",
			},
			want: "needs terms",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, c.files))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error does not mention %q:\n%v", c.want, err)
			}
		})
	}
}

func TestLoadShippedConfig(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	reg, err := Load(filepath.Join(filepath.Dir(file), "..", "..", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Check(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sec-edgar", "fed-press"} {
		if _, ok := reg[id]; !ok {
			t.Errorf("shipped source %q missing", id)
		}
	}
}
