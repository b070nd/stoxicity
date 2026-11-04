package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
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
	a     *app
	fails int
}

func (d *doctor) report(level, name, format string, args ...any) {
	if level == levelFail {
		d.fails++
	}
	fmt.Fprintf(d.a.stdout, "%-4s  %-14s %s\n", level, name, oneLine(fmt.Sprintf(format, args...)))
}

// cmdDoctor checks the installation. It returns an error if any check fails.
func cmdDoctor(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 {
		return usageErrorf("takes no arguments")
	}
	d := &doctor{a: a}
	d.config()
	d.timezone()
	d.dataDir()
	d.envFiles()
	d.sources()
	d.tenants()
	d.contact()
	d.laya(ctx)
	if d.fails > 0 {
		fmt.Fprintf(a.stdout, "\n%d checks failed\n", d.fails)
		return errReported
	}
	return nil
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
	ok, problems := checkWatches(d.a.watches(d.a.tenantID), d.a.watchDir(), nil)
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
