package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/readiness"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Levels of a doctor line.
const (
	levelOK   = "OK"
	levelWarn = "WARN"
	levelFail = "FAIL"
)

const layaTimeout = 3 * time.Second

// doctor prints one line per check and counts the failures.
type doctor struct {
	a      *app
	fails  int
	quiet  bool // collect the checks without printing them (JSON output)
	checks []readiness.Check
	schema readiness.Schema
}

// report records one check. The state follows the level unless the check says
// better: a passed probe is verified, a failed one failed, and a warning about
// something that was not looked at is unknown.
func (d *doctor) report(level, name, format string, args ...any) {
	st := readiness.StateVerified
	detail := oneLine(fmt.Sprintf(format, args...))
	switch level {
	case levelFail:
		st = readiness.StateFailed
	case levelWarn:
		st = readiness.StateStale
		if strings.HasPrefix(detail, "not checked") {
			st = readiness.StateUnknown
		}
	}
	d.reportState(level, st, name, "%s", detail)
}

func (d *doctor) reportState(level string, st readiness.State, name, format string, args ...any) {
	if level == levelFail {
		d.fails++
	}
	detail := oneLine(fmt.Sprintf(format, args...))
	d.checks = append(d.checks, readiness.Check{Name: name, Level: level, State: st, Detail: detail})
	if !d.quiet {
		fmt.Fprintf(d.a.stdout, "%-4s  %-14s %s\n", level, name, detail)
	}
}

// cmdDoctor checks the installation. It returns an error if any check fails.
func cmdDoctor(ctx context.Context, a *app, args []string) error {
	fs := newFlags("doctor")
	scopeFlag := fs.String("scope", "", "also list the capabilities of a scope: research, portfolio or broker")
	asJSON := fs.Bool("json", false, "print the report as JSON (scope defaults to research)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("takes no arguments, only -scope and -json")
	}
	var scope readiness.Scope
	if *scopeFlag != "" || *asJSON {
		name := *scopeFlag
		if name == "" {
			name = string(readiness.Research)
		}
		if scope, err = readiness.ParseScope(name); err != nil {
			return usageErrorf("%v", err)
		}
	}
	// Inspecting the installation must not create or migrate the databases; the
	// schema check below reads them without opening them for writing.
	a.noStore = true
	d := &doctor{a: a, quiet: *asJSON}
	d.config()
	d.timezone()
	d.dataDir()
	d.envFiles()
	d.sources()
	d.tenants()
	d.schemaCheck(ctx)
	d.contact()
	d.laya(ctx)
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(readiness.Build(version, scope, d.checks, d.schema)); err != nil {
			return err
		}
		if d.fails > 0 {
			return errReported
		}
		return nil
	}
	if scope != "" {
		writeCapabilities(a, scope)
	}
	if d.fails > 0 {
		fmt.Fprintf(a.stdout, "\n%d checks failed\n", d.fails)
		return errReported
	}
	return nil
}

// schemaCheck compares the schema of the databases on disk with the newest one
// this binary knows, without creating, migrating or writing anything. A
// database that is behind is migrated, with no way back, by the first command
// that opens it, so that is said before it happens.
func (d *doctor) schemaCheck(ctx context.Context) {
	if d.a.cfgErr != nil {
		d.report(levelWarn, "schema", "not checked: the configuration did not load")
		return
	}
	known, err := store.KnownSchema()
	if err != nil {
		d.report(levelFail, "schema", "%v", err)
		return
	}
	d.schema.KnownBase, d.schema.KnownTenant = known.Base, known.Tenant
	if d.a.cfg.DatabaseURL != "" {
		d.postgresSchema(ctx, known)
		return
	}
	base, err := store.ReadSchema(ctx, filepath.Join(d.a.dataDir(), "base.db"))
	if err != nil {
		d.report(levelFail, "schema", "base.db: %v", err)
		return
	}
	d.schema.BaseExists, d.schema.AppliedBase = base.Exists, base.Applied
	switch {
	case !base.Exists:
		d.reportState(levelOK, readiness.StateImplemented, "schema", "base.db is not created yet; the first run creates it at schema %d", known.Base)
	case base.Applied > known.Base:
		d.report(levelFail, "schema", "base.db is at schema %d, newer than this binary (%d); do not open it with this binary", base.Applied, known.Base)
	case base.Applied < known.Base:
		d.schema.PendingBase = known.Base - base.Applied
		d.reportState(levelWarn, readiness.StateStale, "schema", "base.db is at schema %d and this binary is at %d; the first command that opens it migrates it and there is no way back: run ./backup.sh backup and ./backup.sh verify first", base.Applied, known.Base)
	default:
		d.report(levelOK, "schema", "base.db is at schema %d", base.Applied)
	}
}

// postgresSchema is schemaCheck for a Postgres database: it connects, reads the
// migration tables of the base and tenant schemas, and writes nothing. A server
// that cannot be reached is a failure, never a quiet fall back to the SQLite
// files. The address and the password are not printed.
func (d *doctor) postgresSchema(ctx context.Context, known store.SchemaVersions) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.InspectPostgres(ctx, d.a.cfg.DatabaseURL, false)
	if err != nil {
		d.report(levelFail, "schema", "Postgres (STOXICITY_DATABASE_URL) cannot be reached or read: %s", redactURL(err.Error(), d.a.cfg.DatabaseURL))
		return
	}
	applied, ok := st.Schemas["base"]
	d.schema.BaseExists, d.schema.AppliedBase = ok, applied
	tenants, behind := 0, 0
	for s, v := range st.Schemas {
		if s == "base" {
			continue
		}
		tenants++
		if v > known.Tenant {
			d.report(levelFail, "schema", "tenant schema %s is at %d, newer than this binary (%d); do not open it with this binary", s, v, known.Tenant)
			return
		}
		if v < known.Tenant {
			behind++
		}
	}
	switch {
	case !ok:
		d.reportState(levelOK, readiness.StateImplemented, "schema", "Postgres: the base schema is not created yet; the first run creates it at schema %d", known.Base)
	case applied > known.Base:
		d.report(levelFail, "schema", "Postgres: base is at schema %d, newer than this binary (%d); do not open it with this binary", applied, known.Base)
	case applied < known.Base || behind > 0:
		d.schema.PendingBase = known.Base - applied
		d.reportState(levelWarn, readiness.StateStale, "schema", "Postgres: base is at schema %d and this binary is at %d (%d tenant schema(s) behind); the first command that opens it migrates it: run ./stoxicity ops backup first", applied, known.Base, behind)
	default:
		d.report(levelOK, "schema", "Postgres: base is at schema %d, %d tenant schema(s) current", applied, tenants)
	}
}

// redactURL removes the password of a database URL from text that may quote it.
func redactURL(text, rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			text = strings.ReplaceAll(text, pw, "***")
		}
	}
	return text
}

// writeCapabilities prints the readiness of one scope as a table. The five
// dimensions stay separate columns and nothing is summed into a score.
func writeCapabilities(a *app, scope readiness.Scope) {
	caps := readiness.InScope(readiness.Inventory(), scope)
	t := cliTable{Header: []string{"capability", "story"}}
	for _, dim := range readiness.Dimensions {
		t.Header = append(t.Header, string(dim))
	}
	t.Header = append(t.Header, "blocker")
	for _, c := range caps {
		row := []cliCell{plain(c.ID), plain(c.Story)}
		for _, dim := range readiness.Dimensions {
			l := c.Level(dim)
			st := cliPlain
			switch l {
			case readiness.Yes:
				st = cliGreen
			case readiness.No:
				st = cliRed
			case readiness.Unknown:
				st = cliYellow
			case readiness.NotApplicable:
				st = cliDim
			}
			row = append(row, styled(string(l), st))
		}
		row = append(row, plain(c.Blocker))
		t.Rows = append(t.Rows, row)
	}
	fmt.Fprintf(a.stdout, "\ncapabilities, scope %s (a yes names the file that supports it; unknown means nobody recorded an answer)\n", scope)
	t.write(a.stdout, useColor(a.stdout))
	gaps := readiness.Gaps()
	fmt.Fprintf(a.stdout, "\nknown gaps\n")
	for _, g := range gaps {
		fmt.Fprintf(a.stdout, "  %s (%s): %s\n", g.ID, g.Story, g.Summary)
	}
}

func (d *doctor) config() {
	if d.a.cfgErr != nil {
		d.report(levelFail, "config", "%v", d.a.cfgErr)
		return
	}
	d.report(levelOK, "config", "config.yaml loaded from %s", d.a.cfgDir)
}

func (d *doctor) timezone() {
	if d.a.cfgErr != nil {
		d.report(levelWarn, "timezone", "not checked: the configuration did not load")
		return
	}
	if _, err := time.LoadLocation(d.a.cfg.Timezone); err != nil {
		d.report(levelFail, "timezone", "%q does not load: %v", d.a.cfg.Timezone, err)
		return
	}
	d.report(levelOK, "timezone", "%s", d.a.cfg.Timezone)
}

// dataDir checks that the database folder can be created and written to.
func (d *doctor) dataDir() {
	if d.a.cfgErr != nil {
		d.report(levelWarn, "data dir", "not checked: the configuration did not load")
		return
	}
	dir := d.a.dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		d.report(levelFail, "data dir", "%v", err)
		return
	}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		d.report(levelFail, "data dir", "%s is not writable: %v", dir, err)
		return
	}
	f.Close()
	os.Remove(f.Name())
	d.report(levelOK, "data dir", "%s is writable", dir)
}

// envFiles checks the permissions of .env.local. Loose permissions are a
// warning, and the files are then not loaded at all, which the line says.
func (d *doctor) envFiles() {
	path := filepath.Join(d.a.baseDir, ".env.local")
	info, err := os.Stat(path)
	loose := false
	switch {
	case errors.Is(err, fs.ErrNotExist):
		d.report(levelOK, ".env.local", "not present in %s", d.a.baseDir)
	case err != nil:
		d.report(levelFail, ".env.local", "%v", err)
	case info.Mode().Perm()&0o077 != 0:
		loose = true
		d.report(levelWarn, ".env.local", "%s is readable by group or others (mode %04o); run: chmod 600 %s; the environment files were not loaded", path, info.Mode().Perm(), path)
	default:
		d.report(levelOK, ".env.local", "mode %04o", info.Mode().Perm())
	}
	if d.a.envErr != nil && !loose {
		d.report(levelFail, "env files", "%v", d.a.envErr)
	}
}

func (d *doctor) sources() {
	if d.a.regErr != nil {
		d.report(levelFail, "sources", "%v", d.a.regErr)
		return
	}
	enabled := len(d.a.reg.Enabled())
	d.report(levelOK, "sources", "%d sources, %d enabled", len(d.a.reg), enabled)
}

// tenants checks the tenant files and the watches of the selected tenant.
func (d *doctor) tenants() {
	if d.a.cfgErr != nil {
		d.report(levelWarn, "tenants", "not checked: the configuration did not load")
		return
	}
	list, err := d.a.tenants().List()
	if err != nil {
		d.report(levelFail, "tenants", "%v", err)
		return
	}
	d.report(levelOK, "tenants", "%d tenants", len(list))
	if _, err := d.a.tenants().Get(d.a.tenantID); err != nil {
		d.report(levelFail, "tenant", "%v", err)
		return
	}
	ok, problems := checkWatches(d.a.watches(d.a.tenantID), d.a.watchDir(), nil, d.a.filterEnv(d.a.tenantID))
	for _, p := range problems {
		d.report(levelFail, "watches", "%v", p)
	}
	if len(problems) == 0 {
		d.report(levelOK, "watches", "%d watches of tenant %s", len(ok), d.a.tenantID)
	}
}

// contact checks the address that goes into the User-Agent. Without it nothing
// can be fetched, which is a failure only if a source is switched on.
func (d *doctor) contact() {
	if d.a.cfgErr != nil {
		d.report(levelWarn, "contact", "not checked: the configuration did not load")
		return
	}
	if _, err := httpx.UserAgent(userAgentApp, version, d.a.cfg.Fetch.Contact); err == nil {
		d.report(levelOK, "contact", "set")
		return
	}
	level := levelWarn
	if len(d.a.reg.Enabled()) > 0 {
		level = levelFail
	}
	d.report(level, "contact", "STOXICITY_CONTACT is empty or not an email address or https URL; fetching will refuse to start")
}

// laya checks that the scoring sidecar answers, if it is switched on. It is
// optional: without it the rules score alone, so a missing answer is a warning.
// The address and the key are never printed.
func (d *doctor) laya(ctx context.Context) {
	if d.a.cfgErr != nil || !d.a.cfg.Laya.Enabled {
		return
	}
	s, err := d.a.layaScorer()
	if err != nil {
		d.report(levelWarn, "laya", "the laya settings are not usable: %v; the rules score alone", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, layaTimeout)
	defer cancel()
	if err := s.Ping(ctx); err != nil {
		d.report(levelWarn, "laya", "no answer (%v); the rules score alone. Start the sidecar with LAYA_HOST=127.0.0.1 laya-serve, and set LAYA_API_KEY for it and STOXICITY_LAYA_API_KEY in .env.local to the same value", err)
		return
	}
	d.report(levelOK, "laya", "answers")
}
