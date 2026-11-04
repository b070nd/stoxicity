package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/classify"
	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/entity"
	"github.com/b070nd/stoxicity/app/src/envfile"
	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/ledger"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/lens/laya"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/pipeline"
	"github.com/b070nd/stoxicity/app/src/sources"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/tenant"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// userAgentApp is the product name in the User-Agent.
const userAgentApp = "stoxicity"

// usageError is a mistake in the command line. It makes the program print the
// usage and exit with code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// errReported means the command already printed what went wrong. run exits
// with code 1 and prints nothing more.
var errReported = errors.New("reported")

// runOptions let a test replace what the real program takes from its
// surroundings. The zero value is the real program.
type runOptions struct {
	// cwd is where the search for the repository starts; the working
	// directory if empty.
	cwd string
	// client replaces the HTTP client built from the settings.
	client httpx.Client
	// now replaces the clock.
	now func() time.Time
	// rand replaces the source of the scheduler's jitter.
	rand func() float64
	// tick replaces the minute between two deliveries of deferred alerts and
	// digests in the run command.
	tick time.Duration
	// enricher replaces the laya client when laya is switched on in the
	// settings.
	enricher pipeline.Enricher
	// stdin replaces the standard input, which classify reads for "-".
	stdin io.Reader
}

// app is everything a command works with. Commands take it explicitly so a
// test can build one.
type app struct {
	stdout, stderr io.Writer
	opts           runOptions

	// Command line.
	configRoot string // -config-dir, or the folder found by searching upwards
	envDir     string // folder of the .env files, "" if none was found
	envFiles   envfile.Files
	tenantFlag string
	verbose    bool

	// Results of the start-up steps. A failed step is kept so that doctor can
	// report it; every other command stops on the first one.
	envErr error
	cfg    config.Config
	cfgErr error
	reg    sources.Registry
	regErr error

	cfgDir   string // folder holding config.yaml
	baseDir  string // relative paths in the settings start here
	tenantID string
	log      *slog.Logger

	db *store.DB

	// The second scorer is built on first use, and its failure reported once.
	enr     pipeline.Enricher
	enrDone bool
}

// run is the whole program. It returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, opts runOptions) int {
	a := &app{stdout: stdout, stderr: stderr, opts: opts}
	defer a.close()

	fs := flag.NewFlagSet("stoxicity", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.configRoot, "config-dir", "", "configuration folder (default: found by searching upwards)")
	fs.Var(&a.envFiles, "env-file", "extra environment file, read after .env.local (repeatable)")
	fs.StringVar(&a.tenantFlag, "tenant", "", "tenant to use (default: $STOXICITY_TENANT, then \"default\")")
	fs.BoolVar(&a.verbose, "v", false, "log debug lines")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.usage(stdout)
			return exitOK
		}
		fmt.Fprintf(stderr, "stoxicity: %v\n", err)
		a.usage(stderr)
		return exitUsage
	}
	if *showVersion {
		fmt.Fprintln(stdout, "stoxicity", version)
		return exitOK
	}

	name := fs.Arg(0)
	if name == "" {
		fmt.Fprintln(stderr, "stoxicity: no command given")
		a.usage(stderr)
		return exitUsage
	}
	cmd, ok := lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "stoxicity: unknown command %q\n", name)
		a.usage(stderr)
		return exitUsage
	}

	if cmd.bootstrap {
		a.bootstrap()
		if !cmd.tolerant {
			if err := a.startupError(); err != nil {
				fmt.Fprintf(stderr, "stoxicity: %v\n", err)
				var ue *usageError
				if errors.As(err, &ue) {
					return exitUsage
				}
				return exitError
			}
		}
	}
	err := cmd.run(ctx, a, fs.Args()[1:])
	var ue *usageError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, errReported):
		return exitError
	case errors.As(err, &ue):
		fmt.Fprintf(stderr, "stoxicity: %s: %v\n", name, ue)
		fmt.Fprintf(stderr, "usage: stoxicity [flags] %s\n", cmd.usage)
		return exitUsage
	default:
		fmt.Fprintf(stderr, "stoxicity: %s: %v\n", name, err)
		return exitError
	}
}

// close releases the database, if a command opened it.
func (a *app) close() {
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			fmt.Fprintf(a.stderr, "stoxicity: close store: %v\n", err)
		}
	}
}

// now is the current time.
func (a *app) now() time.Time {
	if a.opts.now != nil {
		return a.opts.now()
	}
	return time.Now()
}

// bootstrap does the start-up steps: find the folders, load the environment
// files, then the configuration and the sources. It never fails; the results
// are kept in the app.
func (a *app) bootstrap() {
	cwd := a.opts.cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if a.configRoot == "" {
		a.configRoot = findUp(cwd, hasConfig)
		if a.configRoot == "" {
			a.configRoot = cwd
		}
	}
	a.cfgDir = config.Dir(a.configRoot)
	a.envDir = findUp(cwd, hasEnvFile)
	envBase := a.envDir
	if envBase == "" {
		envBase = a.configRoot
	}
	a.baseDir = envBase

	// The sources name the variables that hold their keys, and the environment
	// files may set those, so the sources are read first. Their errors are kept
	// for later; here only the names matter.
	var allow []string
	if reg, err := sources.Load(a.cfgDir); err == nil {
		for _, s := range reg {
			if s.AuthEnv != "" {
				allow = append(allow, s.AuthEnv)
			}
		}
	}
	boot := slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a.envErr = envfile.Setup(a.envFiles, envBase, allow, boot)

	a.log = a.newLogger()
	a.cfg, a.cfgErr = config.Load(a.configRoot)
	a.reg, a.regErr = sources.Load(a.cfgDir)

	def := tenant.Default
	if a.cfgErr == nil {
		def = a.cfg.Tenancy.Default
	}
	a.tenantID = tenant.Resolve(a.tenantFlag, os.Getenv("STOXICITY_TENANT"), def)
}

// startupError returns the first start-up step that failed.
func (a *app) startupError() error {
	switch {
	case a.envErr != nil:
		return a.envErr
	case a.cfgErr != nil:
		return a.cfgErr
	case a.regErr != nil:
		return a.regErr
	case !model.ValidID(a.tenantID):
		return usageErrorf("bad tenant ID %q", a.tenantID)
	}
	return nil
}

// newLogger builds the logger from -v and STOXICITY_LOG_LEVEL.
func (a *app) newLogger() *slog.Logger {
	level := slog.LevelInfo
	if v := os.Getenv("STOXICITY_LOG_LEVEL"); v != "" {
		if err := level.UnmarshalText([]byte(v)); err != nil {
			fmt.Fprintln(a.stderr, "stoxicity: STOXICITY_LOG_LEVEL must be debug, info, warn or error; using info")
			level = slog.LevelInfo
		}
	}
	if a.verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: level}))
}

// findUp returns the closest folder, starting at dir and going up, for which
// match is true, or "" if there is none.
func findUp(dir string, match func(dir string) bool) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if match(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hasConfig reports whether dir is a configuration root, as config.Dir sees it.
func hasConfig(dir string) bool {
	for _, p := range []string{"app/config/config.yaml", "config/config.yaml", "config.yaml"} {
		if exists(filepath.Join(dir, p)) {
			return true
		}
	}
	return false
}

// hasEnvFile reports whether dir holds a .env or .env.local file.
func hasEnvFile(dir string) bool {
	return exists(filepath.Join(dir, ".env")) || exists(filepath.Join(dir, ".env.local"))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// location is the time zone of days and digests.
func (a *app) location() *time.Location { return a.cfg.Location() }

// dataDir is the folder of the database. A relative setting starts at the
// folder of the .env file.
func (a *app) dataDir() string {
	d := a.cfg.DataDir
	if !filepath.IsAbs(d) {
		d = filepath.Join(a.baseDir, d)
	}
	return d
}

// tenantsDir is the folder that holds one folder per tenant.
func (a *app) tenantsDir() string {
	d := a.cfg.Tenancy.Dir
	if !filepath.IsAbs(d) {
		d = filepath.Join(a.cfgDir, d)
	}
	return d
}

// tenants finds the tenants on disk.
func (a *app) tenants() tenant.FSRegistry { return tenant.FSRegistry{Dir: a.tenantsDir()} }

// watches returns the watch store of one tenant.
func (a *app) watches(tenantID string) watch.FSStore {
	return watch.FSStore{Dir: filepath.Join(a.tenantsDir(), tenantID, "watches")}
}

// requireTenant checks that the selected tenant exists.
func (a *app) requireTenant() (tenant.Tenant, error) {
	t, err := a.tenants().Get(a.tenantID)
	if err != nil {
		return tenant.Tenant{}, err
	}
	return t, nil
}

// store opens the database on first use.
func (a *app) store() (*store.DB, error) {
	if a.db != nil {
		return a.db, nil
	}
	db, err := store.Open(a.dataDir())
	if err != nil {
		return nil, err
	}
	a.db = db
	return db, nil
}

// visible lists the sources the selected tenant may see.
func (a *app) visible() map[string]bool { return a.reg.VisibleTo(a.tenantID) }

// lens builds the rule-based lens with the source tiers and the 8-K item table.
func (a *app) lens() *lens.Lens {
	return lens.New(a.cfg.Lens, lens.Options{
		SourceTier: func(id string) model.Tier { return a.reg[id].Tier },
		ItemKind:   edgar.Kinds,
	})
}

// classifier builds the topic and sector classifier, or returns nil when the
// settings define no topics and no sectors.
func (a *app) classifier() pipeline.Classifier {
	if len(a.cfg.Topics)+len(a.cfg.Sectors) == 0 {
		return nil
	}
	return classify.New(a.cfg.Topics, a.cfg.Sectors)
}

// layaScorer builds the client of the laya sidecar from the settings and
// STOXICITY_LAYA_API_KEY. Its errors name neither the address nor the key.
func (a *app) layaScorer() (*laya.Scorer, error) {
	var topics, sectors []string
	for _, t := range a.cfg.Topics {
		topics = append(topics, t.Name)
	}
	for _, s := range a.cfg.Sectors {
		sectors = append(sectors, s.Name)
	}
	return laya.New(laya.Config{
		URL:       a.cfg.Laya.URL,
		Model:     a.cfg.Laya.Model,
		APIKey:    os.Getenv("STOXICITY_LAYA_API_KEY"),
		Timeout:   a.cfg.LayaTimeout(),
		BatchSize: a.cfg.Laya.BatchSize,
		Topics:    topics,
		Sectors:   sectors,
	})
}

// enricher returns the second scorer, or nil when laya is switched off. If the
// settings cannot make a client it says so once on stderr, naming neither the
// address nor the key, and the program goes on with the rules alone.
func (a *app) enricher() pipeline.Enricher {
	if a.enrDone || !a.cfg.Laya.Enabled {
		return a.enr
	}
	a.enrDone = true
	if a.opts.enricher != nil {
		a.enr = a.opts.enricher
		return a.enr
	}
	s, err := a.layaScorer()
	if err != nil {
		fmt.Fprintf(a.stderr, "stoxicity: laya is switched on but unusable (%v); scoring with the rules alone\n", err)
		return nil
	}
	a.enr = policyEnricher{Enricher: s, policy: layaPolicy(a.cfg.Laya)}
	return a.enr
}

// policyEnricher adds the merge policy to the version of the second scorer. A
// stored score is never overwritten for a version it already has, so without
// this a change of laya.apply, laya.min_confidence or laya.relevance_weight
// would leave older articles on the old policy even after a rescore.
type policyEnricher struct {
	pipeline.Enricher
	policy string
}

// Version is the scorer's version followed by the policy fingerprint.
func (p policyEnricher) Version() string { return p.Enricher.Version() + "-p" + p.policy }

// layaPolicy fingerprints the settings that decide how laya's answers change a
// score: the fields applied, the confidence gate and the relevance weight.
func layaPolicy(l config.Laya) string {
	apply := slices.Clone(l.Apply)
	slices.Sort(apply)
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%g|%g", strings.Join(apply, ","), l.MinConfidence, l.RelevanceWeight))
	return hex.EncodeToString(sum[:4])
}

// layaUse says which answers of the second scorer change the score.
func layaUse(l config.Laya) lens.Use {
	return lens.Use{
		Kind:            l.Applies("kind"),
		Tone:            l.Applies("tone"),
		Sector:          l.Applies("sector"),
		Topics:          l.Applies("topics"),
		Materiality:     l.Applies("materiality"),
		Relevance:       l.Applies("relevance"),
		RelevanceWeight: l.RelevanceWeight,
	}
}

// pipeline builds the ingest pipeline on the opened store.
func (a *app) pipeline(db store.Store) *pipeline.Pipeline {
	return pipeline.New(pipeline.Config{
		Store:         db,
		Lens:          a.lens(),
		Classifier:    a.classifier(),
		Entities:      a.entities(),
		Enricher:      a.enricher(),
		Use:           layaUse(a.cfg.Laya),
		MinConfidence: a.cfg.Laya.MinConfidence,
		Logger:        a.log,
		Now:           a.now,
	})
}

// ledger builds the hit ledger on the opened store, sending through channels
// (nil means no sender: a command that only evaluates or previews needs none).
func (a *app) ledger(db store.Store, channels map[channel.Kind]channel.Channel) *ledger.Ledger {
	return ledger.New(ledger.Config{
		Store:     db,
		Sources:   a.reg,
		Watches:   func(id string) watch.Store { return a.watches(id) },
		Channels:  channels,
		Threshold: a.cfg.Lens.Threshold,
		Zone:      a.location(),
		Now:       a.now,
		Logger:    a.log,
	})
}

// channels builds the senders for the channel kinds that the watches of every
// tenant use, from the STOXICITY_FILE_DIR, STOXICITY_FEED_PATH,
// STOXICITY_FEED_TITLE, STOXICITY_FEED_URL and STOXICITY_WEBHOOK_URL variables.
// A relative folder or file starts at the folder of the .env file, as the data
// folder does. A channel that cannot be built is reported on stderr, naming the
// variable that is missing and never its value, and is left out: the ledger
// treats an alert for it as delivered, so fetching goes on.
func (a *app) channels() map[channel.Kind]channel.Channel {
	var kinds []channel.Kind
	seen := map[channel.Kind]bool{}
	add := func(ks []channel.Kind) {
		for _, k := range ks {
			if !seen[k] {
				seen[k] = true
				kinds = append(kinds, k)
			}
		}
	}
	if ts, err := a.tenants().List(); err == nil {
		for _, t := range ts {
			ws, err := a.watches(t.ID).List()
			if err != nil {
				continue
			}
			for _, w := range ws {
				for _, al := range w.Alerts {
					add(al.Channels)
				}
				add(w.Digest.Channels)
			}
		}
	}
	slices.Sort(kinds)
	built, errs := channel.Build(kinds, channel.Settings{
		FileDir:    a.relative(os.Getenv("STOXICITY_FILE_DIR")),
		FeedPath:   a.relative(os.Getenv("STOXICITY_FEED_PATH")),
		FeedTitle:  os.Getenv("STOXICITY_FEED_TITLE"),
		FeedURL:    os.Getenv("STOXICITY_FEED_URL"),
		WebhookURL: os.Getenv("STOXICITY_WEBHOOK_URL"),
	}, nil)
	for _, err := range errs {
		fmt.Fprintf(a.stderr, "stoxicity: %v\n", err)
	}
	return built
}

// relative resolves a path from the settings: an empty one stays empty and a
// relative one starts at the folder of the .env file.
func (a *app) relative(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(a.baseDir, p)
}

// entities knows the companies the user follows: the watchlist and the tickers
// that the tenants' watches name. There is no company master list yet, so a
// mention is found by its ticker only.
func (a *app) entities() entity.Resolver {
	var tickers []string
	tickers = append(tickers, a.cfg.Watchlist...)
	if ts, err := a.tenants().List(); err == nil {
		for _, t := range ts {
			ws, err := a.watches(t.ID).List()
			if err != nil {
				continue
			}
			for _, w := range ws {
				tickers = append(tickers, w.Scope.Tickers...)
			}
		}
	}
	slices.Sort(tickers)
	var list []model.Entity
	for _, t := range slices.Compact(tickers) {
		if t = strings.ToUpper(strings.TrimSpace(t)); t != "" {
			list = append(list, model.Entity{Ticker: t})
		}
	}
	return entity.New(list)
}

// httpClient returns the client for fetching. It refuses to run without a
// contact address, because the User-Agent must say who is asking.
func (a *app) httpClient() (httpx.Client, error) {
	ua, err := httpx.UserAgent(userAgentApp, version, a.cfg.Fetch.Contact)
	if err != nil {
		if strings.TrimSpace(a.cfg.Fetch.Contact) == "" {
			return nil, errors.New("no contact address: set STOXICITY_CONTACT (in .env.local) to your email or an https URL; sources such as the SEC block clients that do not identify themselves")
		}
		return nil, errors.New("STOXICITY_CONTACT is not usable: it must be an email address or an https URL, without spaces or parentheses")
	}
	if a.opts.client != nil {
		return a.opts.client, nil
	}
	c, err := httpx.New(httpx.Config{
		UserAgent: ua,
		Timeout:   a.cfg.FetchTimeout(),
		Burst:     a.cfg.Refresh.PerHost,
		// The SEC allows 10 requests a second per client across its hosts.
		Groups:    map[string]string{"www.sec.gov": "sec", "data.sec.gov": "sec"},
		HostRates: map[string]float64{"sec": 10},
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// parseDay reads YYYY-MM-DD as midnight in loc.
func parseDay(s string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation(time.DateOnly, s, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a day in the form YYYY-MM-DD", s)
	}
	return t, nil
}

// parseSince reads a duration such as 36h or 7d.
func parseSince(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err == nil && fmt.Sprint(n) == days && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a duration such as 24h or 7d", s)
	}
	return d, nil
}
