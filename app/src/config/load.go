package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/quota"
	"go.yaml.in/yaml/v3"
)

const (
	fileName   = "config.yaml"
	dateLayout = "2006-01-02"

	// maxRules and maxTerms bound the topic and sector lists so a typo cannot
	// make every article scan thousands of patterns.
	maxRules = 200 // topics, and sectors, each
	maxTerms = 100 // words, and tickers, in one rule

	// maxLayaBatch bounds laya.batch_size: one request carries that many
	// articles.
	maxLayaBatch = 64
)

// Dir returns the folder under root that holds config.yaml. It checks
// root/app/config, then root/config, then root, and returns the first that
// contains config.yaml. If none does, it returns root/app/config.
func Dir(root string) string {
	candidates := []string{
		filepath.Join(root, "app", "config"),
		filepath.Join(root, "config"),
		root,
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, fileName)); err == nil {
			return dir
		}
	}
	return filepath.Join(root, "app", "config")
}

// Load reads config.yaml from Dir(root). Keys the file leaves out keep the
// repository defaults, and an unknown key is an error. Then the STOXICITY_*
// environment variables override the matching settings; an empty variable
// overrides nothing. Load reports every invalid setting at once.
//
// The YAML is read with go.yaml.in/yaml/v3. The archived gopkg.in/yaml.v3 is
// not used.
func Load(root string) (Config, error) {
	path := filepath.Join(Dir(root), fileName)
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	defer f.Close()

	cfg := defaults()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}

	applyEnv(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// defaults returns the settings of the repository's config.yaml, except that
// Fetch.Contact is empty and the second scoring stage is off.
func defaults() Config {
	return Config{
		Since:     "2026-11-01",
		Timezone:  "Europe/Berlin",
		DataDir:   "./data",
		Watchlist: []string{},
		Lens: Lens{
			Threshold: 0.5,
			Boost: []string{
				"earnings", "annual report", "10-K", "guidance", "free cash flow",
				"competitive advantage", "moat", "buyback", "dividend",
				"insider buying", "spin-off", "capital allocation",
			},
			Demote: []string{
				"price target", "intraday", "options flow", "short squeeze",
				"meme stock", "technical analysis",
			},
		},
		Fetch: Fetch{Interval: "30m", Timeout: "20s"},
		Refresh: Refresh{
			Enabled: true, MaxBackoff: "6h", PerHost: 2, Workers: 16,
			Fast: FastRefresh{
				Interval: "2m", Zone: "America/New_York",
				From: "06:00", To: "22:00", WeekdaysOnly: true,
			},
		},
		Laya: Laya{
			Enabled:         false,
			URL:             "http://127.0.0.1:8000",
			MinConfidence:   0,
			Timeout:         "60s",
			RelevanceWeight: 0.3,
			BatchSize:       16,
			Manage:          true,
			Command:         "laya-serve",
			StartTimeout:    "3m",
			Device:          "cpu",
			Offline:         true,
		},
		Company: Company{Enabled: true, MaxAge: "168h", Listing: true, RefreshEvery: "6h", EnrichPerCycle: 200, SitesPerCycle: 400, Quotes: "polygon", QuotesPerCycle: 10, Fundamentals: "sec", Insiders: "sec", InsidersPerCycle: 300, CoversPerCycle: 20, FeedSearchPerCycle: 60},
		Tenancy: Tenancy{Dir: "tenants", Default: "default"},
	}
}

// applyEnv replaces settings with the STOXICITY_* variables that are set and
// not empty.
var deviceName = regexp.MustCompile(`^[a-z]+(:[0-9]+)?$`)

func applyEnv(cfg *Config) {
	override("STOXICITY_TIMEZONE", &cfg.Timezone)
	override("STOXICITY_DATA_DIR", &cfg.DataDir)
	override("STOXICITY_CONTACT", &cfg.Fetch.Contact)
	override("STOXICITY_POLYGON_KEY", &cfg.Company.PolygonKey)
	if cfg.Company.PolygonKey == "" { // Polygon.io is now called Massive, and its keys carry that name
		override("MASSIVE_API_KEY", &cfg.Company.PolygonKey)
	}
	override("STOXICITY_LAYA_URL", &cfg.Laya.URL)
	override("STOXICITY_LAYA_COMMAND", &cfg.Laya.Command)
	override("STOXICITY_TENANT", &cfg.Tenancy.Default)
}

// override sets field to the value of the environment variable name, unless
// that variable is empty.
func override(name string, field *string) {
	if v := os.Getenv(name); v != "" {
		*field = v
	}
}

// validate returns every problem with cfg, joined, or nil if there is none.
// It does not echo the Laya URL, which may carry a credential.
func validate(cfg Config) error {
	var errs []error

	if _, err := time.Parse(dateLayout, cfg.Since); err != nil {
		errs = append(errs, fmt.Errorf("config: since %q is not a YYYY-MM-DD date", cfg.Since))
	}

	// LoadLocation accepts "" as UTC, so an empty zone is rejected here.
	if cfg.Timezone == "" {
		errs = append(errs, errors.New("config: timezone is empty"))
	} else if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("config: timezone %q is not a known IANA zone", cfg.Timezone))
	}

	checkDuration := func(key, value string) {
		d, err := time.ParseDuration(value)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("config: %s %q is not a duration", key, value))
		case d <= 0:
			errs = append(errs, fmt.Errorf("config: %s must be greater than zero, got %q", key, value))
		}
	}
	checkDuration("fetch.interval", cfg.Fetch.Interval)
	checkDuration("fetch.timeout", cfg.Fetch.Timeout)
	for key, q := range cfg.Fetch.Quotas {
		period, err := time.ParseDuration(q.Period)
		if !quota.ValidKey(key) || err != nil || q.Requests <= 0 || (quota.Limit{Requests: q.Requests, Period: period}).Validate() != nil {
			errs = append(errs, errors.New("config: fetch.quotas needs lower-case provider hosts/groups, positive requests and a period between 1ms and 8784h"))
		}
	}
	checkDuration("refresh.max_backoff", cfg.Refresh.MaxBackoff)
	checkDuration("laya.timeout", cfg.Laya.Timeout)
	checkDuration("laya.start_timeout", cfg.Laya.StartTimeout)
	if cfg.Laya.Threads < 0 || cfg.Laya.Threads > 256 {
		errs = append(errs, fmt.Errorf("config: laya.threads must be between 0 and 256, got %d", cfg.Laya.Threads))
	}
	if d := cfg.Laya.Device; d != "" && !deviceName.MatchString(d) {
		errs = append(errs, fmt.Errorf("config: laya.device %q is not a torch device name such as cpu, mps or cuda:0", d))
	}
	checkDuration("company.max_age", cfg.Company.MaxAge)
	if !strings.EqualFold(strings.TrimSpace(cfg.Company.RefreshEvery), "off") {
		checkDuration("company.refresh_every", cfg.Company.RefreshEvery)
		if d := parseDuration(cfg.Company.RefreshEvery); d != 0 && d < time.Minute {
			errs = append(errs, fmt.Errorf("config: company.refresh_every must be at least 1m, or \"off\""))
		}
	}
	if cfg.Company.EnrichPerCycle < 0 || cfg.Company.EnrichPerCycle > 5000 {
		errs = append(errs, fmt.Errorf("config: company.enrich_per_cycle must be between 0 and 5000, got %d", cfg.Company.EnrichPerCycle))
	}

	if !slices.Contains([]string{"polygon", "off"}, cfg.Company.Quotes) {
		errs = append(errs, fmt.Errorf("config: company.quotes must be polygon or off, got %q", cfg.Company.Quotes))
	}
	if !slices.Contains([]string{"sec", "off"}, cfg.Company.Fundamentals) {
		errs = append(errs, fmt.Errorf("config: company.fundamentals must be sec or off, got %q", cfg.Company.Fundamentals))
	}
	if !slices.Contains([]string{"sec", "off"}, cfg.Company.Insiders) {
		errs = append(errs, fmt.Errorf("config: company.insiders must be sec or off, got %q", cfg.Company.Insiders))
	}
	if cfg.Company.InsidersPerCycle < 0 || cfg.Company.InsidersPerCycle > 5000 {
		errs = append(errs, fmt.Errorf("config: company.insiders_per_cycle must be between 0 and 5000, got %d", cfg.Company.InsidersPerCycle))
	}
	if cfg.Company.CoversPerCycle < 0 || cfg.Company.CoversPerCycle > 500 {
		errs = append(errs, fmt.Errorf("config: company.covers_per_cycle must be between 0 and 500, got %d", cfg.Company.CoversPerCycle))
	}
	if cfg.Company.QuotesPerCycle < 0 || cfg.Company.QuotesPerCycle > 5000 {
		errs = append(errs, fmt.Errorf("config: company.quotes_per_cycle must be between 0 and 5000, got %d", cfg.Company.QuotesPerCycle))
	}
	if cfg.Company.SitesPerCycle < 0 || cfg.Company.SitesPerCycle > 5000 {
		errs = append(errs, fmt.Errorf("config: company.sites_per_cycle must be between 0 and 5000, got %d", cfg.Company.SitesPerCycle))
	}
	if cfg.Company.FeedSearchPerCycle < 0 || cfg.Company.FeedSearchPerCycle > 2000 {
		errs = append(errs, fmt.Errorf("config: company.feed_search_per_cycle must be between 0 and 2000, got %d", cfg.Company.FeedSearchPerCycle))
	}

	checkUnit := func(key string, v float64) {
		if !(v >= 0 && v <= 1) { // also rejects NaN
			errs = append(errs, fmt.Errorf("config: %s must be between 0 and 1, got %g", key, v))
		}
	}
	checkUnit("lens.threshold", cfg.Lens.Threshold)
	checkUnit("laya.min_confidence", cfg.Laya.MinConfidence)
	checkUnit("laya.relevance_weight", cfg.Laya.RelevanceWeight)
	if cfg.Laya.BatchSize < 1 || cfg.Laya.BatchSize > maxLayaBatch {
		errs = append(errs, fmt.Errorf("config: laya.batch_size must be between 1 and %d, got %d", maxLayaBatch, cfg.Laya.BatchSize))
	}
	errs = append(errs, checkApply(cfg.Laya.Apply)...)

	if cfg.Laya.Enabled {
		u, err := url.Parse(cfg.Laya.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("config: laya.url is not a valid http or https URL"))
		}
	}

	if !model.ValidID(cfg.Tenancy.Default) {
		errs = append(errs, fmt.Errorf("config: tenancy.default %q is not a valid tenant ID", cfg.Tenancy.Default))
	}

	if cfg.Refresh.PerHost < 1 {
		errs = append(errs, fmt.Errorf("config: refresh.per_host must be at least 1, got %d", cfg.Refresh.PerHost))
	}

	if cfg.Refresh.Workers < 1 || cfg.Refresh.Workers > 64 {
		errs = append(errs, fmt.Errorf("config: refresh.workers must be between 1 and 64, got %d", cfg.Refresh.Workers))
	}

	errs = append(errs, checkFast(cfg.Refresh.Fast)...)

	errs = append(errs, checkRules("topics", topicTerms(cfg.Topics))...)
	errs = append(errs, checkRules("sectors", sectorTerms(cfg.Sectors))...)

	return errors.Join(errs...)
}

// checkApply returns the problems with laya.apply: names that are not a
// field of the second scorer, and names listed twice.
func checkApply(apply []string) []error {
	var errs []error
	seen := make(map[string]bool, len(apply))
	for _, name := range apply {
		switch {
		case !slices.Contains(LayaFields, name):
			errs = append(errs, fmt.Errorf("config: laya.apply %q is not one of %s", name, strings.Join(LayaFields, ", ")))
		case seen[name]:
			errs = append(errs, fmt.Errorf("config: laya.apply lists %q twice", name))
		}
		seen[name] = true
	}
	return errs
}

// checkFast returns the problems with the settings of refresh.fast. It checks
// them whether or not the feature is enabled, so a typo shows up before it is
// switched on.
func checkFast(f FastRefresh) []error {
	var errs []error
	if d, err := time.ParseDuration(f.Interval); err != nil {
		errs = append(errs, fmt.Errorf("config: refresh.fast.interval %q is not a duration", f.Interval))
	} else if d < time.Minute {
		errs = append(errs, fmt.Errorf("config: refresh.fast.interval must be at least 1m, got %q", f.Interval))
	}
	if f.Zone == "" {
		errs = append(errs, errors.New("config: refresh.fast.zone is empty"))
	} else if _, err := time.LoadLocation(f.Zone); err != nil {
		errs = append(errs, fmt.Errorf("config: refresh.fast.zone %q is not a known IANA zone", f.Zone))
	}
	from, okFrom := minutesOfDay(f.From)
	to, okTo := minutesOfDay(f.To)
	if !okFrom {
		errs = append(errs, fmt.Errorf("config: refresh.fast.from %q is not a time as HH:MM", f.From))
	}
	if !okTo {
		errs = append(errs, fmt.Errorf("config: refresh.fast.to %q is not a time as HH:MM", f.To))
	}
	if okFrom && okTo && to <= from {
		errs = append(errs, fmt.Errorf("config: refresh.fast.to %q must be later than from %q", f.To, f.From))
	}
	return errs
}

// ruleTerms is the part of a topic or sector rule that validation reads.
type ruleTerms struct {
	name           string
	words, tickers []string
}

func topicTerms(rules []TopicRule) []ruleTerms {
	out := make([]ruleTerms, len(rules))
	for i, r := range rules {
		out[i] = ruleTerms{r.Name, r.Words, r.Tickers}
	}
	return out
}

func sectorTerms(rules []SectorRule) []ruleTerms {
	out := make([]ruleTerms, len(rules))
	for i, r := range rules {
		out[i] = ruleTerms{r.Name, r.Words, r.Tickers}
	}
	return out
}

// checkRules returns the problems with one list of topic or sector rules; key
// is "topics" or "sectors". Names must be unique within the list.
func checkRules(key string, rules []ruleTerms) []error {
	var errs []error
	if len(rules) > maxRules {
		errs = append(errs, fmt.Errorf("config: %s has %d rules, at most %d allowed", key, len(rules), maxRules))
	}
	seen := make(map[string]bool, len(rules))
	for i, r := range rules {
		where := fmt.Sprintf("%s[%d]", key, i)
		switch {
		case r.name == "":
			errs = append(errs, fmt.Errorf("config: %s has no name", where))
		case !model.ValidID(r.name):
			errs = append(errs, fmt.Errorf("config: %s name %q is not a lower-case slug (letters, digits, hyphens)", where, r.name))
		case seen[r.name]:
			errs = append(errs, fmt.Errorf("config: %s name %q is used twice", where, r.name))
		default:
			where = fmt.Sprintf("%s %q", key, r.name)
		}
		seen[r.name] = true
		if len(r.words)+len(r.tickers) == 0 {
			errs = append(errs, fmt.Errorf("config: %s needs at least one word or ticker", where))
		}
		if len(r.words) > maxTerms {
			errs = append(errs, fmt.Errorf("config: %s has %d words, at most %d allowed", where, len(r.words), maxTerms))
		}
		if len(r.tickers) > maxTerms {
			errs = append(errs, fmt.Errorf("config: %s has %d tickers, at most %d allowed", where, len(r.tickers), maxTerms))
		}
		for _, w := range r.words {
			if strings.TrimSpace(w) == "" {
				errs = append(errs, fmt.Errorf("config: %s has a blank word", where))
			}
		}
		for _, t := range r.tickers {
			if t == "" || t != strings.ToUpper(t) || strings.ContainsAny(t, " \t\n") {
				errs = append(errs, fmt.Errorf("config: %s ticker %q is not an upper-case symbol", where, t))
			}
		}
	}
	return errs
}
