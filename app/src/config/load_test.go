package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// envNames are the variables Load reads. Tests blank them so the developer's
// shell cannot change the result.
var envNames = []string{
	"STOXICITY_TIMEZONE",
	"STOXICITY_DATA_DIR",
	"STOXICITY_CONTACT",
	"STOXICITY_LAYA_URL",
	"STOXICITY_TENANT",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range envNames {
		t.Setenv(name, "")
	}
}

// writeFile creates root/rel with body, creating parent folders.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeConfig creates a temporary root whose config/config.yaml holds body.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "config/config.yaml", body)
	return root
}

func TestLoadRepositoryConfig(t *testing.T) {
	clearEnv(t)
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// This file is app/src/config/load_test.go; the module root is app/.
	root := filepath.Join(filepath.Dir(file), "..", "..")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", root, err)
	}
	if cfg.Timezone != "Europe/Berlin" {
		t.Errorf("Timezone = %q, want Europe/Berlin", cfg.Timezone)
	}
	if !cfg.Laya.Enabled {
		t.Error("Laya.Enabled = false, want true")
	}
	if cfg.Fetch.Contact != "" {
		t.Errorf("Fetch.Contact = %q, want empty", cfg.Fetch.Contact)
	}
	want := Laya{
		Enabled: true, URL: "http://127.0.0.1:8000", MinConfidence: 0.7, Timeout: "60s",
		Apply: []string{"kind", "topics", "tone"}, RelevanceWeight: 0.3, BatchSize: 16,
		Manage: true, Command: "laya-serve", StartTimeout: "3m",
		Device: "cpu", Offline: true,
	}
	if !reflect.DeepEqual(cfg.Laya, want) {
		t.Errorf("Laya = %+v, want %+v", cfg.Laya, want)
	}
	for _, f := range []string{"kind", "topics", "tone"} {
		if !cfg.Laya.Applies(f) {
			t.Errorf("Laya.Applies(%q) = false", f)
		}
	}
	for _, f := range []string{"sector", "materiality", "relevance"} {
		if cfg.Laya.Applies(f) {
			t.Errorf("Laya.Applies(%q) = true, want advisory only", f)
		}
	}
}

func TestLayaDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeConfig(t, "laya:\n  enabled: true\n"))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	l := cfg.Laya
	if l.Timeout != "60s" || l.RelevanceWeight != 0.3 || l.BatchSize != 16 || len(l.Apply) != 0 {
		t.Errorf("Laya defaults = %+v, want 60s, weight 0.3, batch 16, nothing applied", l)
	}
	if cfg.LayaTimeout() != time.Minute {
		t.Errorf("LayaTimeout() = %v, want 1m", cfg.LayaTimeout())
	}
}

func TestLoadPartialFileGetsDefaults(t *testing.T) {
	clearEnv(t)
	root := writeConfig(t, "since: 2026-12-01\nfetch:\n  contact: me@example.com\nrefresh:\n  per_host: 3\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Since != "2026-12-01" {
		t.Errorf("Since = %q, want 2026-12-01", cfg.Since)
	}
	if cfg.Fetch.Contact != "me@example.com" {
		t.Errorf("Fetch.Contact = %q, want me@example.com", cfg.Fetch.Contact)
	}
	if cfg.Fetch.Interval != "30m" || cfg.Fetch.Timeout != "20s" {
		t.Errorf("Fetch = %+v, want default interval and timeout", cfg.Fetch)
	}
	if !cfg.Refresh.Enabled || cfg.Refresh.MaxBackoff != "6h" || cfg.Refresh.PerHost != 3 {
		t.Errorf("Refresh = %+v, want enabled, 6h, per_host 3", cfg.Refresh)
	}
	if cfg.Timezone != "Europe/Berlin" || cfg.DataDir != "./data" {
		t.Errorf("Timezone, DataDir = %q, %q, want defaults", cfg.Timezone, cfg.DataDir)
	}
	if cfg.Lens.Threshold != 0.5 || len(cfg.Lens.Boost) != 12 || len(cfg.Lens.Demote) != 6 {
		t.Errorf("Lens = %+v, want default threshold and word lists", cfg.Lens)
	}
	if cfg.Tenancy.Dir != "tenants" || cfg.Tenancy.Default != "default" {
		t.Errorf("Tenancy = %+v, want defaults", cfg.Tenancy)
	}
}

func TestLoadEmptyFileGetsDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Since != "2026-11-01" || cfg.Fetch.Interval != "30m" {
		t.Errorf("Load of empty file = %+v, want defaults", cfg)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	clearEnv(t)
	_, err := Load(writeConfig(t, "fetch:\n  intervl: 5m\n"))
	if err == nil {
		t.Fatal("Load accepted an unknown key")
	}
	for _, want := range []string{"config.yaml", "intervl"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestEnvOverrides(t *testing.T) {
	tests := []struct {
		env   string
		value string
		get   func(Config) string
	}{
		{"STOXICITY_TIMEZONE", "America/New_York", func(c Config) string { return c.Timezone }},
		{"STOXICITY_DATA_DIR", "/var/stoxicity", func(c Config) string { return c.DataDir }},
		{"STOXICITY_CONTACT", "me@example.com", func(c Config) string { return c.Fetch.Contact }},
		{"STOXICITY_LAYA_URL", "https://laya.example.com", func(c Config) string { return c.Laya.URL }},
		{"STOXICITY_TENANT", "work", func(c Config) string { return c.Tenancy.Default }},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tt.env, tt.value)
			// The file sets a different value so the override is visible.
			root := writeConfig(t, "timezone: Asia/Tokyo\ndata_dir: /file\nfetch:\n  contact: file@example.com\nlaya:\n  url: http://file.example.com\ntenancy:\n  default: file\n")
			cfg, err := Load(root)
			if err != nil {
				t.Fatalf("Load error: %v", err)
			}
			if got := tt.get(cfg); got != tt.value {
				t.Errorf("%s: got %q, want %q", tt.env, got, tt.value)
			}
		})
	}
}

func TestEmptyEnvIsNotOverride(t *testing.T) {
	clearEnv(t) // every variable is set to ""
	cfg, err := Load(writeConfig(t, "timezone: Asia/Tokyo\nfetch:\n  contact: file@example.com\n"))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Timezone != "Asia/Tokyo" || cfg.Fetch.Contact != "file@example.com" {
		t.Errorf("empty env overrode file: Timezone %q, Contact %q", cfg.Timezone, cfg.Fetch.Contact)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // substring of the error; empty means the file is valid
	}{
		{"valid laya url ignored when disabled", "laya:\n  url: ftp://x\n", ""},
		{"since out of range", "since: 2026-13-01\n", "since"},
		{"since wrong format", "since: 01/11/2026\n", "since"},
		{"timezone unknown", "timezone: Mars/Olympus\n", "timezone"},
		{"timezone empty", "timezone: \"\"\n", "timezone"},
		{"fetch interval not a duration", "fetch:\n  interval: soon\n", "fetch.interval"},
		{"fetch timeout zero", "fetch:\n  timeout: 0s\n", "fetch.timeout"},
		{"max backoff negative", "refresh:\n  max_backoff: -1h\n", "refresh.max_backoff"},
		{"laya timeout bad", "laya:\n  timeout: 5 seconds\n", "laya.timeout"},
		{"lens threshold above one", "lens:\n  threshold: 1.5\n", "lens.threshold"},
		{"laya min confidence below zero", "laya:\n  min_confidence: -0.1\n", "laya.min_confidence"},
		{"laya url scheme when enabled", "laya:\n  enabled: true\n  url: ftp://x\n", "laya.url"},
		{"laya url empty when enabled", "laya:\n  enabled: true\n  url: \"\"\n", "laya.url"},
		{"laya apply valid", "laya:\n  apply: [kind, tone, sector, topics, materiality, relevance]\n", ""},
		{"laya apply empty", "laya:\n  apply: []\n", ""},
		{"laya apply unknown name", "laya:\n  apply: [kind, mood]\n", "laya.apply \"mood\""},
		{"laya apply wrong case", "laya:\n  apply: [Kind]\n", "laya.apply"},
		{"laya apply duplicate", "laya:\n  apply: [tone, kind, tone]\n", "twice"},
		{"laya weight above one", "laya:\n  relevance_weight: 1.2\n", "laya.relevance_weight"},
		{"laya weight negative", "laya:\n  relevance_weight: -0.1\n", "laya.relevance_weight"},
		{"laya weight bounds", "laya:\n  relevance_weight: 1\n", ""},
		{"laya batch zero", "laya:\n  batch_size: 0\n", "laya.batch_size"},
		{"laya batch too large", "laya:\n  batch_size: 65\n", "laya.batch_size"},
		{"laya batch largest", "laya:\n  batch_size: 64\n", ""},
		{"laya batch smallest", "laya:\n  batch_size: 1\n", ""},
		{"tenant id invalid", "tenancy:\n  default: \"bad id!\"\n", "tenancy.default"},
		{"per host zero", "refresh:\n  per_host: 0\n", "refresh.per_host"},
		{"fast refresh valid", "refresh:\n  fast:\n    enabled: true\n    interval: 90s\n    from: \"07:15\"\n    to: \"20:00\"\n", ""},
		{"fast interval not a duration", "refresh:\n  fast:\n    interval: often\n", "refresh.fast.interval"},
		{"fast interval too short", "refresh:\n  fast:\n    interval: 20s\n", "refresh.fast.interval"},
		{"fast zone unknown", "refresh:\n  fast:\n    zone: Mars/Olympus\n", "refresh.fast.zone"},
		{"fast zone empty", "refresh:\n  fast:\n    zone: \"\"\n", "refresh.fast.zone"},
		{"fast from bad", "refresh:\n  fast:\n    from: \"6am\"\n", "refresh.fast.from"},
		{"fast to bad", "refresh:\n  fast:\n    to: \"25:00\"\n", "refresh.fast.to"},
		{"fast window backwards", "refresh:\n  fast:\n    from: \"22:00\"\n    to: \"06:00\"\n", "refresh.fast.to"},
		{"fast unknown key", "refresh:\n  fast:\n    weekends: true\n", "weekends"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			_, err := Load(writeConfig(t, tt.body))
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Load error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load accepted invalid config, want error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestValidationReportsAllProblems(t *testing.T) {
	clearEnv(t)
	_, err := Load(writeConfig(t, "since: nope\ntimezone: Mars/Olympus\nrefresh:\n  per_host: 0\n"))
	if err == nil {
		t.Fatal("Load accepted three invalid settings")
	}
	for _, want := range []string{"since", "timezone", "refresh.per_host"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("error %T is not a joined error", err)
	}
	if n := len(joined.Unwrap()); n != 3 {
		t.Errorf("got %d errors, want 3", n)
	}
}

func TestDir(t *testing.T) {
	tests := []struct {
		name  string
		files []string // config.yaml paths relative to the root
		want  string   // expected folder relative to the root
	}{
		{"app/config", []string{"app/config/config.yaml"}, "app/config"},
		{"config", []string{"config/config.yaml"}, "config"},
		{"root", []string{"config.yaml"}, ""},
		{"app/config wins over config", []string{"app/config/config.yaml", "config/config.yaml"}, "app/config"},
		{"folder without config.yaml is skipped", []string{"app/config/other.yaml", "config/config.yaml"}, "config"},
		{"none found", nil, "app/config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range tt.files {
				writeFile(t, root, f, "")
			}
			want := filepath.Join(root, tt.want)
			if got := Dir(root); got != want {
				t.Errorf("Dir(root) = %q, want %q", got, want)
			}
		})
	}
}

func TestAccessors(t *testing.T) {
	cfg := Config{
		Since:    "2026-11-01",
		Timezone: "Europe/Berlin",
		Fetch:    Fetch{Interval: "30m", Timeout: "20s"},
		Refresh:  Refresh{MaxBackoff: "6h"},
		Laya:     Laya{Timeout: "5s"},
	}
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Location(); got.String() != loc.String() {
		t.Errorf("Location() = %q, want Europe/Berlin", got)
	}
	if got, want := cfg.SinceTime(), time.Date(2026, 11, 1, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("SinceTime() = %v, want %v", got, want)
	}
	durations := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"FetchInterval", cfg.FetchInterval(), 30 * time.Minute},
		{"FetchTimeout", cfg.FetchTimeout(), 20 * time.Second},
		{"MaxBackoff", cfg.MaxBackoff(), 6 * time.Hour},
		{"LayaTimeout", cfg.LayaTimeout(), 5 * time.Second},
	}
	for _, d := range durations {
		if d.got != d.want {
			t.Errorf("%s() = %v, want %v", d.name, d.got, d.want)
		}
	}
}

func TestAccessorsOnUnvalidatedConfig(t *testing.T) {
	cfg := Config{Timezone: "Mars/Olympus", Since: "yesterday"}
	if cfg.Location() != time.UTC {
		t.Errorf("Location() = %q, want UTC for an unknown zone", cfg.Location())
	}
	if !cfg.SinceTime().IsZero() {
		t.Errorf("SinceTime() = %v, want zero time for a bad date", cfg.SinceTime())
	}
	if cfg.FetchInterval() != 0 || cfg.FetchTimeout() != 0 || cfg.MaxBackoff() != 0 || cfg.LayaTimeout() != 0 {
		t.Error("durations on an empty Config are not zero")
	}
}

func TestLoadTopicsAndSectors(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeConfig(t, `
topics:
  - name: ai-infrastructure
    words: [data center, gpu]
    tickers: [NVDA]
  - name: housing
    words: [mortgage rates]
sectors:
  - name: technology
    words: [software]
    tickers: [MSFT, AAPL]
`))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	wantTopics := []TopicRule{
		{Name: "ai-infrastructure", Words: []string{"data center", "gpu"}, Tickers: []string{"NVDA"}},
		{Name: "housing", Words: []string{"mortgage rates"}},
	}
	if !reflect.DeepEqual(cfg.Topics, wantTopics) {
		t.Errorf("Topics = %+v, want %+v", cfg.Topics, wantTopics)
	}
	wantSectors := []SectorRule{{Name: "technology", Words: []string{"software"}, Tickers: []string{"MSFT", "AAPL"}}}
	if !reflect.DeepEqual(cfg.Sectors, wantSectors) {
		t.Errorf("Sectors = %+v, want %+v", cfg.Sectors, wantSectors)
	}
}

func TestTopicsAndSectorsDefaultToNone(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(cfg.Topics) != 0 || len(cfg.Sectors) != 0 {
		t.Errorf("Topics, Sectors = %v, %v, want none", cfg.Topics, cfg.Sectors)
	}
}

func TestRepositoryConfigHasTaxonomy(t *testing.T) {
	clearEnv(t)
	_, file, _, _ := runtime.Caller(0)
	cfg, err := Load(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(cfg.Topics) < 14 {
		t.Errorf("shipped config has %d topics, want at least 14", len(cfg.Topics))
	}
	var names []string
	for _, s := range cfg.Sectors {
		names = append(names, s.Name)
	}
	want := []string{
		"technology", "health-care", "financials", "consumer-discretionary", "consumer-staples", "energy",
		"utilities", "industrials", "materials", "real-estate", "communication-services",
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("sector names = %v, want the 11 GICS sectors %v", names, want)
	}
}

func TestRuleValidation(t *testing.T) {
	manyWords := "[" + strings.TrimSuffix(strings.Repeat("w, ", 101), ", ") + "]"
	var manyRules strings.Builder
	manyRules.WriteString("topics:\n")
	for i := 0; i < 201; i++ {
		fmt.Fprintf(&manyRules, "  - {name: t%d, words: [x]}\n", i)
	}
	tests := []struct {
		name string
		body string
		want string // substring of the error; empty means the file is valid
	}{
		{"valid", "topics:\n  - {name: a-b1, words: [x y]}\nsectors:\n  - {name: s, tickers: [AAPL]}\n", ""},
		{"same name in topics and sectors", "topics:\n  - {name: energy, words: [x]}\nsectors:\n  - {name: energy, words: [x]}\n", ""},
		{"topic without name", "topics:\n  - {words: [x]}\n", "has no name"},
		{"topic upper case name", "topics:\n  - {name: AI, words: [x]}\n", "lower-case slug"},
		{"topic name with space", "topics:\n  - {name: \"ai infra\", words: [x]}\n", "lower-case slug"},
		{"topic name with slash", "topics:\n  - {name: a/b, words: [x]}\n", "lower-case slug"},
		{"duplicate topic", "topics:\n  - {name: a, words: [x]}\n  - {name: a, words: [y]}\n", "used twice"},
		{"duplicate sector", "sectors:\n  - {name: a, words: [x]}\n  - {name: a, words: [y]}\n", "used twice"},
		{"topic without words or tickers", "topics:\n  - {name: a}\n", "at least one word or ticker"},
		{"sector without words or tickers", "sectors:\n  - {name: a, words: []}\n", "at least one word or ticker"},
		{"blank word", "topics:\n  - {name: a, words: [x, \"  \"]}\n", "blank word"},
		{"lower-case ticker", "topics:\n  - {name: a, tickers: [aapl]}\n", "upper-case"},
		{"ticker with space", "sectors:\n  - {name: a, tickers: [\"AA PL\"]}\n", "upper-case"},
		{"too many words", "topics:\n  - {name: a, words: " + manyWords + "}\n", "at most 100"},
		{"too many rules", manyRules.String(), "at most 200"},
		{"unknown key in rule", "topics:\n  - {name: a, wrds: [x]}\n", "wrds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			_, err := Load(writeConfig(t, tt.body))
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Load error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load accepted invalid rules, want error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestFastRefreshDefaultsAndRepositoryConfig(t *testing.T) {
	clearEnv(t)
	want := FastRefresh{Interval: "2m", Zone: "America/New_York", From: "06:00", To: "22:00", WeekdaysOnly: true}
	cfg, err := Load(writeConfig(t, "since: 2026-11-01\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Refresh.Fast != want {
		t.Errorf("default Fast = %+v, want %+v", cfg.Refresh.Fast, want)
	}
	_, file, _, _ := runtime.Caller(0)
	repo, err := Load(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if repo.Refresh.Fast != want {
		t.Errorf("repository Fast = %+v, want the disabled defaults %+v", repo.Refresh.Fast, want)
	}
}

func TestIntervalForFilingsWindow(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	cfg := Config{Refresh: Refresh{Fast: FastRefresh{
		Enabled: true, Interval: "2m", Zone: "America/New_York", From: "06:00", To: "22:00", WeekdaysOnly: true,
	}}}
	filings := model.Source{ID: "edgar", Kind: model.KindFilings, Interval: "10m"}
	rss := model.Source{ID: "news", Kind: model.KindRSS}
	utc := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, time.UTC) }
	const fast = 2 * time.Minute

	// New York changes to summer time on Sunday 8 March 2026 at 02:00: 06:00
	// local is 11:00 UTC in the winter week and 10:00 UTC in the summer week.
	cases := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{"Friday before the change, 05:59 EST", utc(2026, 3, 6, 10, 59), 0},
		{"Friday before the change, 06:00 EST", utc(2026, 3, 6, 11, 0), fast},
		{"Monday after the change, 05:59 EDT", utc(2026, 3, 9, 9, 59), 0},
		{"Monday after the change, 06:00 EDT", utc(2026, 3, 9, 10, 0), fast},
		{"Monday after the change, the old 06:00 EST hour is 07:00 EDT", utc(2026, 3, 9, 11, 0), fast},
		{"Friday before the change, 21:59 EST", utc(2026, 3, 7, 2, 59), fast},
		{"Friday before the change, 22:00 EST", utc(2026, 3, 7, 3, 0), 0},
		{"Monday after the change, 21:59 EDT", utc(2026, 3, 10, 1, 59), fast},
		{"Monday after the change, 22:00 EDT", utc(2026, 3, 10, 2, 0), 0},
		// Back to standard time on Sunday 1 November 2026 at 02:00.
		{"Friday before the autumn change, 06:00 EDT", utc(2026, 10, 30, 10, 0), fast},
		{"Monday after the autumn change, 05:59 EST", utc(2026, 11, 2, 10, 59), 0},
		{"Monday after the autumn change, 06:00 EST", utc(2026, 11, 2, 11, 0), fast},
		{"Saturday midday", utc(2026, 3, 7, 17, 0), 0},
		{"Sunday midday", utc(2026, 3, 8, 17, 0), 0},
		{"midnight UTC on Monday is still Sunday evening in New York", utc(2026, 3, 9, 1, 0), 0},
	}
	for _, c := range cases {
		if got := cfg.IntervalFor(filings, c.now); got != c.want {
			t.Errorf("%s (%s): IntervalFor = %v, want %v", c.name, c.now.In(ny).Format("Mon 15:04 MST"), got, c.want)
		}
	}

	if got := cfg.IntervalFor(rss, utc(2026, 3, 9, 15, 0)); got != 0 {
		t.Errorf("an rss source got %v, want 0 (only filings are fast)", got)
	}
	weekends := cfg
	weekends.Refresh.Fast.WeekdaysOnly = false
	if got := weekends.IntervalFor(filings, utc(2026, 3, 7, 17, 0)); got != fast {
		t.Errorf("Saturday with weekdays_only false: %v, want %v", got, fast)
	}
	off := cfg
	off.Refresh.Fast.Enabled = false
	if got := off.IntervalFor(filings, utc(2026, 3, 9, 15, 0)); got != 0 {
		t.Errorf("disabled: %v, want 0", got)
	}
	var zero Config
	if got := zero.IntervalFor(filings, utc(2026, 3, 9, 15, 0)); got != 0 {
		t.Errorf("zero Config: %v, want 0", got)
	}
	bad := cfg
	bad.Refresh.Fast.Zone = "Mars/Olympus"
	if got := bad.IntervalFor(filings, utc(2026, 3, 9, 15, 0)); got != 0 {
		t.Errorf("unknown zone: %v, want 0", got)
	}
}
