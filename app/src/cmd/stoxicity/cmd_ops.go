package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/ops"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
)

// cmdOps runs and recovers the installation: health and metrics without
// opening a database, a coherent backup of every database with a manifest,
// rotation and an independent copy, a restore that keeps what it replaces and
// holds back new orders until the broker is reconciled, service files, and
// drills that measure all of this on copies.
func cmdOps(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: health | metrics | backup | list | verify | restore | recover | reconciled | drill | service | leases")
	}
	// Everything here works on files first. Nothing opens a database through the
	// store (which would migrate it) unless the command says so.
	a.noStore = true
	switch args[0] {
	case "health":
		return opsHealth(ctx, a, args[1:])
	case "metrics":
		return opsMetrics(ctx, a, args[1:])
	case "backup":
		return opsBackup(ctx, a, args[1:])
	case "list":
		return opsList(ctx, a, args[1:])
	case "verify":
		return opsVerify(ctx, a, args[1:])
	case "restore":
		return opsRestore(ctx, a, args[1:])
	case "recover":
		return opsRecover(ctx, a, args[1:])
	case "reconciled":
		return opsReconciled(ctx, a, args[1:])
	case "drill":
		return opsDrill(ctx, a, args[1:])
	case "service":
		return opsService(ctx, a, args[1:])
	case "leases":
		return opsLeases(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (health, metrics, backup, list, verify, restore, recover, reconciled, drill, service, leases)", args[0])
}

// restoreSkipProcessCheck is for tests, which share a machine with the user's
// own running program.
var restoreSkipProcessCheck = false

// backupRoot is where backups go: STOXICITY_BACKUP_DIR, or "backups" beside the
// configuration folder, which is where backup.sh puts them.
func (a *app) backupRoot() string {
	if d := os.Getenv("STOXICITY_BACKUP_DIR"); d != "" {
		return a.relative(d)
	}
	return filepath.Join(a.baseDir, "backups")
}

// quota reads the limits from STOXICITY_MIN_FREE_MB and STOXICITY_MAX_DATA_MB.
// A value that is not a whole number of megabytes is an error, not "no limit".
func (a *app) quota() (ops.Quota, error) {
	var q ops.Quota
	for _, f := range []struct {
		name string
		dst  *uint64
	}{{"STOXICITY_MIN_FREE_MB", &q.MinFree}, {"STOXICITY_MAX_DATA_MB", &q.MaxData}} {
		v := strings.TrimSpace(os.Getenv(f.name))
		if v == "" {
			continue
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return q, usageErrorf("%s must be a whole number of megabytes", f.name)
		}
		*f.dst = n << 20
	}
	return q, nil
}

// guardDisk refuses heavy work when the disk or the data folder is at its
// limit. Cancelling, reconciling and delivering an alert never call it.
func (a *app) guardDisk(need uint64) error {
	q, err := a.quota()
	if err != nil {
		return err
	}
	return q.Check(a.dataDir(), need)
}

func (a *app) leases() ops.Leases {
	return ops.Leases{Dir: filepath.Join(a.dataDir(), "leases"), Now: a.now}
}

// holder names this process in a lease file.
func holder() string { return fmt.Sprintf("pid %d", os.Getpid()) }

func opsHealth(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops health")
	asJSON := fs.Bool("json", false, "print JSON")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	q, err := a.quota()
	if err != nil {
		return err
	}
	h, err := ops.Check(ctx, ops.HealthOptions{DataDir: a.dataDir(), PostgresURL: a.cfg.DatabaseURL, BackupRoot: a.backupRoot(), Quota: q, Now: a.now})
	if err != nil {
		return err
	}
	if *asJSON {
		if err := printJSON(a, h); err != nil {
			return err
		}
	} else {
		st := map[string]cliStyle{ops.OK: cliGreen, ops.Warn: cliYellow, ops.Fail: cliRed}[h.Status]
		fmt.Fprintf(a.stdout, "health: %s\n", ansiOr(a, strings.ToUpper(h.Status), st))
		t := cliTable{Header: []string{"database", "size", "wal", "schema", "state"}, Right: []bool{false, true, true, true, false}}
		for _, d := range h.DBs {
			stl := cliGreen
			if d.State != "current" {
				stl = cliYellow
				if d.State == "ahead" || d.State == "unreadable" {
					stl = cliRed
				}
			}
			t.Rows = append(t.Rows, []cliCell{plain(d.Name), plain(strconv.FormatInt(d.Bytes, 10)), plain(strconv.FormatInt(d.WAL, 10)), plain(fmt.Sprintf("%d of %d", d.Applied, d.Known)), styled(d.State, stl)})
		}
		t.write(a.stdout, useColor(a.stdout))
		fmt.Fprintf(a.stdout, "disk: %d MiB free of %d MiB; data folder %d MiB\n", h.Disk.Free>>20, h.Disk.Total>>20, h.Disk.Data>>20)
		if h.Backup.Count == 0 {
			fmt.Fprintln(a.stdout, "backups: none")
		} else {
			fmt.Fprintf(a.stdout, "backups: %d, newest %s (%s old)\n", h.Backup.Count, h.Backup.Newest, (time.Duration(h.Backup.AgeSeconds) * time.Second).Round(time.Minute))
		}
		for _, l := range h.Leases {
			if l.Active {
				fmt.Fprintf(a.stdout, "job %s holds its lease for another %s\n", l.Name, (time.Duration(l.ExpiresIn) * time.Second).Round(time.Second))
			}
		}
		for _, p := range h.Problems {
			fmt.Fprintf(a.stdout, "  %s\n", p)
		}
		if a.cfg.DatabaseURL != "" {
			fmt.Fprintln(a.stdout, "Nothing here names a path, a tenant, an address or a key; Postgres was read (its migration tables) and nothing was written.")
		} else {
			fmt.Fprintln(a.stdout, "Nothing here names a path, a tenant, an address or a key; no database was opened.")
		}
	}
	if h.Status == ops.Fail {
		return errReported
	}
	return nil
}

func ansiOr(a *app, s string, st cliStyle) string {
	if !useColor(a.stdout) {
		return s
	}
	return cliCodes[st] + s + "\x1b[0m"
}

func opsMetrics(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops metrics")
	out := fs.String("out", "", "write to this file (replaced atomically) instead of the screen, for a metrics collector that reads files")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	q, err := a.quota()
	if err != nil {
		return err
	}
	h, err := ops.Check(ctx, ops.HealthOptions{DataDir: a.dataDir(), PostgresURL: a.cfg.DatabaseURL, BackupRoot: a.backupRoot(), Quota: q, Now: a.now})
	if err != nil {
		return err
	}
	text := ops.Metrics(h)
	if *out == "" {
		_, err := fmt.Fprint(a.stdout, text)
		return err
	}
	tmp := *out + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, *out)
}

func opsBackup(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops backup")
	to := fs.String("to", "", "backup folder (default: STOXICITY_BACKUP_DIR, or backups beside the configuration)")
	mirror := fs.String("mirror", "", "a second location to copy the backup to and verify there")
	keep := fs.Int("keep", 0, "after a good backup, keep this many of the newest and remove older ones (0: remove none)")
	weeks := fs.Int("keep-weeks", 0, "also keep the newest backup of each of this many weeks")
	incoherent := fs.Bool("allow-incoherent", false, "keep a backup taken while databases were changing, marked as such")
	asJSON := fs.Bool("json", false, "print JSON")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	root := *to
	if root == "" {
		root = a.backupRoot()
	}
	postgres := a.cfg.DatabaseURL != ""
	size, _ := ops.DirSize(a.dataDir())
	if err := a.guardDisk(size + size/5); err != nil {
		return fmt.Errorf("no backup was made: %w", err)
	}
	lease, err := a.leases().Acquire("backup", holder(), time.Hour)
	if err != nil {
		return err
	}
	defer lease.Release()

	var dest string
	var m ops.Manifest
	if postgres {
		dest, m, err = ops.SnapshotPostgres(ctx, a.cfg.DatabaseURL, a.dataDir(), root, ops.SnapshotOptions{Now: a.now})
	} else {
		dest, m, err = ops.Snapshot(ctx, a.dataDir(), root, ops.SnapshotOptions{Now: a.now, AllowIncoherent: *incoherent})
	}
	if err != nil {
		return err
	}
	rep, err := ops.Verify(ctx, dest)
	if err != nil {
		return err
	}
	if !rep.OK {
		return fmt.Errorf("the backup %s was written but does not verify: %s", m.ID, strings.Join(rep.Problems, "; "))
	}
	res := map[string]any{"backup": m.ID, "files": len(m.Files), "coherent": m.Coherent, "attempts": m.Attempts, "verified": true}
	var mirrored *ops.MirrorResult
	if *mirror != "" {
		mr, err := ops.Mirror(ctx, dest, a.relative(*mirror))
		if err != nil {
			return fmt.Errorf("the backup %s is good but the copy to the mirror failed: %w", m.ID, err)
		}
		mirrored = &mr
		res["mirror"] = map[string]any{"verified": mr.Report.OK, "same_filesystem": mr.SameDevice}
	}
	var removed []string
	if *keep > 0 {
		protect := []string{m.ID}
		removed, err = ops.Rotate(root, *keep, *weeks, protect...)
		if err != nil {
			return err
		}
		res["removed"] = removed
	}
	if *asJSON {
		return printJSON(a, res)
	}
	if postgres {
		fmt.Fprintf(a.stdout, "backup %s: %d files; the Postgres dump was restored into a scratch database (dropped again) and every table counted, and the archive on disk was checked by checksum and listing\n", m.ID, len(m.Files))
	} else {
		fmt.Fprintf(a.stdout, "backup %s: %d files, verified (checksums, integrity, rows of every table)\n", m.ID, len(m.Files))
	}
	if !m.Coherent {
		fmt.Fprintln(a.stdout, "WARNING: the databases changed while they were copied; this backup is not one moment of the data folder.")
	} else if m.Attempts > 1 {
		fmt.Fprintf(a.stdout, "(the set was copied again %d time(s) because a database changed in the middle)\n", m.Attempts-1)
	}
	if mirrored != nil {
		fmt.Fprintf(a.stdout, "mirror: copied and verified at %s\n", filepath.Base(filepath.Dir(mirrored.Path)))
		if mirrored.SameDevice {
			fmt.Fprintln(a.stdout, "WARNING: the mirror is on the same filesystem as the backup. It guards against deletion, not against the disk failing.")
		}
	} else {
		fmt.Fprintln(a.stdout, "No mirror: this backup is on the same machine as the data. Use -mirror with a folder on another disk or machine.")
	}
	if len(removed) > 0 {
		fmt.Fprintf(a.stdout, "removed %d older backup(s): %s\n", len(removed), strings.Join(removed, ", "))
	}
	return nil
}

func opsList(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops list")
	to := fs.String("to", "", "backup folder")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	root := *to
	if root == "" {
		root = a.backupRoot()
	}
	ids, err := ops.List(root)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintln(a.stdout, "no backups")
		return nil
	}
	t := cliTable{Header: []string{"backup", "taken", "files", "coherent"}, Right: []bool{false, false, true, false}}
	for _, id := range ids {
		m, err := ops.ReadManifest(filepath.Join(root, id))
		if err != nil {
			t.Rows = append(t.Rows, []cliCell{plain(id), styled("manifest unreadable", cliRed), plain("-"), plain("-")})
			continue
		}
		c, st := "yes", cliGreen
		if !m.Coherent {
			c, st = "NO", cliRed
		}
		t.Rows = append(t.Rows, []cliCell{plain(id), plain(m.Taken.Format("2006-01-02 15:04")), plain(strconv.Itoa(len(m.Files))), styled(c, st)})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}

func opsVerify(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops verify")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("usage: ops verify BACKUP-FOLDER [-json]")
	}
	rep, err := ops.VerifyWith(ctx, a.backupPath(rest[0]), ops.VerifyOptions{PostgresURL: a.cfg.DatabaseURL})
	if err != nil {
		return err
	}
	if *asJSON {
		if err := printJSON(a, rep); err != nil {
			return err
		}
	} else if rep.OK {
		fmt.Fprintf(a.stdout, "backup %s is intact: %d files, %d bytes, every checksum, integrity check and row count agrees (%s)\n", rep.ID, rep.Files, rep.Bytes, rep.Took.Round(time.Millisecond))
		for _, n := range rep.Notes {
			fmt.Fprintf(a.stdout, "not checked: %s\n", n)
		}
	} else {
		fmt.Fprintf(a.stdout, "backup %s does NOT verify:\n", rep.ID)
		for _, p := range rep.Problems {
			fmt.Fprintf(a.stdout, "  %s\n", p)
		}
	}
	if !rep.OK {
		return errReported
	}
	return nil
}

// backupPath accepts a folder or the ID of a backup in the backup root.
func (a *app) backupPath(s string) string {
	if _, err := os.Stat(s); err == nil || strings.ContainsRune(s, filepath.Separator) {
		return s
	}
	return filepath.Join(a.backupRoot(), s)
}

func opsRestore(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops restore")
	replace := fs.Bool("replace-data", false, "confirm that the data folder is to be replaced (it is kept aside, not deleted)")
	by := fs.String("by", "", "who is restoring")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if a.cfg.DatabaseURL != "" {
		if len(rest) != 1 || strings.TrimSpace(*by) == "" {
			return usageErrorf("usage: ops restore BACKUP -by NAME   (restores into a new database beside the configured one; nothing is replaced)")
		}
		return opsRestorePostgres(ctx, a, rest[0], strings.TrimSpace(*by))
	}
	if len(rest) != 1 || !*replace || strings.TrimSpace(*by) == "" {
		return usageErrorf("usage: ops restore BACKUP -replace-data -by NAME   (stop every other stoxicity process first)")
	}
	dest := a.backupPath(rest[0])
	res, err := ops.Restore(ctx, dest, a.dataDir(), ops.RestoreOptions{Now: a.now, SkipProcessCheck: restoreSkipProcessCheck})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "restored backup %s: %d files, %d bytes (verified before and after copying; %s)\n", res.BackupID, res.Files, res.Bytes, res.Total.Round(time.Millisecond))
	if res.Previous != "" {
		fmt.Fprintf(a.stdout, "the folder it replaced is %s beside the data folder; nothing was deleted\n", res.Previous)
	}
	// New orders that could reach a broker are switched off in every tenant, and
	// all new risk is held back by the restore marker until the broker is reconciled.
	a.noStore = false
	db, err := a.store()
	if err != nil {
		return fmt.Errorf("restored, but the database could not be opened to switch off new orders: %w", err)
	}
	ids, err := tenantIDs(a.dataDir())
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, env := range []portfolio.Env{portfolio.Real, portfolio.PaperBroker} {
			if err := db.Orders(id).SetControl(ctx, env, true, "restored from backup "+res.BackupID, strings.TrimSpace(*by)); err != nil {
				return fmt.Errorf("restored, but new %s orders of %s could not be switched off: %w", env, id, err)
			}
		}
	}
	fmt.Fprintf(a.stdout, "new orders for real and paper_broker books are switched off in %d tenant(s), and new risk is refused until the broker is reconciled.\n", len(ids))
	fmt.Fprintln(a.stdout, "Next: broker sync BOOK for every book bound to a broker, settle the differences, then ops reconciled -by NAME, then order control enable.")
	return nil
}

// opsRestorePostgres restores a backup into a new database. The configured
// database is left as it is; the owner switches to the restored one by setting
// STOXICITY_DATABASE_URL, which keeps the old one as the way back.
func opsRestorePostgres(ctx context.Context, a *app, backup, by string) error {
	if !restoreSkipProcessCheck {
		if pids, known := ops.Running(); !known {
			return errors.New("cannot tell whether the program is running (pgrep is missing); stop it first and run this again")
		} else if len(pids) > 0 {
			return fmt.Errorf("the program is running (process %v); stop it first", pids)
		}
	}
	res, err := ops.RestorePostgres(ctx, a.backupPath(backup), a.cfg.DatabaseURL, ops.RestoreOptions{Now: a.now})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "restored backup %s into the new database %s: %d tables, %d rows, every table equal to the backup (%s)\n", res.BackupID, res.Database, res.Tables, res.Rows, res.Total.Round(time.Millisecond))
	fmt.Fprintf(a.stdout, "the configured database %s was not touched\n", res.Previous)
	m, _ := ops.ReadManifest(a.backupPath(backup))
	if err := os.MkdirAll(a.dataDir(), 0o755); err != nil {
		return err
	}
	if err := ops.WriteRestore(a.dataDir(), ops.RestoreMarker{State: ops.NeedsReconcile, Restored: a.now().UTC(), BackupID: res.BackupID, BackupTook: m.Taken, Previous: res.Previous}); err != nil {
		return err
	}
	// New orders that could reach a broker are switched off in every tenant of
	// the restored database, and new risk is refused until the broker is reconciled.
	db, err := store.OpenPostgres(ctx, res.URL)
	if err != nil {
		return fmt.Errorf("restored, but the new database could not be opened to switch off new orders: %w", err)
	}
	defer db.Close()
	restored := *a
	restored.cfg.DatabaseURL = res.URL
	ids, err := restored.tenantIDList(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, env := range []portfolio.Env{portfolio.Real, portfolio.PaperBroker} {
			if err := db.Orders(id).SetControl(ctx, env, true, "restored from backup "+res.BackupID, by); err != nil {
				return fmt.Errorf("restored, but new %s orders of %s could not be switched off: %w", env, id, err)
			}
		}
	}
	fmt.Fprintf(a.stdout, "new orders for real and paper_broker books are switched off in %d tenant(s), and new risk is refused until the broker is reconciled.\n", len(ids))
	fmt.Fprintf(a.stdout, "To use the restored data, set STOXICITY_DATABASE_URL to the same server and user with the database name %s (in .env.local), then: broker sync BOOK for every book bound to a broker, ops reconciled -by NAME, order control enable.\n", res.Database)
	fmt.Fprintln(a.stdout, "Company files in the backup were not copied; they are in the backup folder.")
	return nil
}

// tenantIDList lists the tenants that have a database: SQLite files in the data
// folder, or the tenant schemas of the configured Postgres database.
func (a *app) tenantIDList(ctx context.Context) ([]string, error) {
	if a.cfg.DatabaseURL == "" {
		return tenantIDs(a.dataDir())
	}
	st, err := store.InspectPostgres(ctx, a.cfg.DatabaseURL, false)
	if err != nil {
		return nil, err
	}
	var ids []string
	for s := range st.Schemas {
		if id, ok := strings.CutPrefix(s, "tenant_"); ok && model.ValidID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func tenantIDs(dataDir string) ([]string, error) {
	dbs, err := ops.Databases(dataDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range dbs {
		if id, ok := strings.CutPrefix(d, "tenants/"); ok {
			id = strings.TrimSuffix(id, ".db")
			if model.ValidID(id) {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

func opsRecover(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 {
		return usageErrorf("ops recover takes no arguments")
	}
	if _, err := os.Stat(a.dataDir()); err == nil {
		fmt.Fprintln(a.stdout, "the data folder is there; nothing to recover")
		return nil
	}
	if err := ops.Recover(a.dataDir()); err != nil {
		return err
	}
	if _, err := os.Stat(a.dataDir()); err != nil {
		return errors.New("the data folder is missing and no earlier restore left one to put back")
	}
	fmt.Fprintln(a.stdout, "an interrupted restore was finished (or undone): the data folder is back. Run ops health.")
	return nil
}

// reconcileUnmet lists what still stands between a restored folder and orders:
// every book bound to a broker needs a broker sync made after the restore and
// no open difference.
func reconcileUnmet(ctx context.Context, db *store.DB, tenants []string, after time.Time) ([]string, int, error) {
	var unmet []string
	bound := 0
	for _, id := range tenants {
		rs := db.Records(id)
		binds, err := rs.Current(ctx, "broker.bind", "")
		if err != nil {
			return nil, 0, err
		}
		for _, b := range binds {
			bound++
			var body struct {
				Book string `json:"book"`
			}
			if err := json.Unmarshal(b.Body, &body); err != nil {
				return nil, 0, fmt.Errorf("binding %s does not decode: %w", b.Key, err)
			}
			book := b.Key
			label := id + "/" + book
			sync, err := rs.Latest(ctx, "broker.sync", book)
			if errors.Is(err, store.ErrNoRecord) {
				unmet = append(unmet, label+": no broker sync yet")
			} else if err != nil {
				return nil, 0, err
			} else {
				var sr struct {
					At time.Time `json:"at"`
				}
				if err := json.Unmarshal(sync.Body, &sr); err != nil {
					return nil, 0, err
				}
				if !sr.At.After(after) {
					unmet = append(unmet, label+": its last broker sync is from before the restore")
				}
			}
			diffs, err := rs.Current(ctx, "broker.difference", book+"/")
			if err != nil {
				return nil, 0, err
			}
			open := 0
			for _, d := range diffs {
				var dr struct {
					State string `json:"state"`
				}
				if json.Unmarshal(d.Body, &dr) == nil && dr.State == "open" {
					open++
				}
			}
			if open > 0 {
				unmet = append(unmet, fmt.Sprintf("%s: %d open difference(s) with the broker", label, open))
			}
		}
	}
	sort.Strings(unmet)
	return unmet, bound, nil
}

func opsReconciled(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops reconciled")
	by := fs.String("by", "", "who has looked at the broker")
	note := fs.String("note", "", "what was found")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if strings.TrimSpace(*by) == "" {
		return usageErrorf("usage: ops reconciled -by NAME [-note TEXT]")
	}
	mk, ok, err := ops.ReadRestore(a.dataDir())
	if err != nil {
		return err
	}
	if !ok || mk.State != ops.NeedsReconcile {
		fmt.Fprintln(a.stdout, "nothing to reconcile: this data folder has no restore waiting")
		return nil
	}
	a.noStore = false
	db, err := a.store()
	if err != nil {
		return err
	}
	ids, err := a.tenantIDList(ctx)
	if err != nil {
		return err
	}
	unmet, bound, err := reconcileUnmet(ctx, db, ids, mk.Restored)
	if err != nil {
		return err
	}
	if len(unmet) > 0 {
		fmt.Fprintln(a.stdout, "not reconciled yet:")
		for _, u := range unmet {
			fmt.Fprintf(a.stdout, "  %s\n", u)
		}
		fmt.Fprintln(a.stdout, "Run broker sync BOOK and settle the differences (broker differences BOOK), then run this again.")
		return errReported
	}
	mk.State, mk.ClearedBy, mk.Cleared, mk.Note = ops.Reconciled, strings.TrimSpace(*by), a.now().UTC(), strings.TrimSpace(*note)
	if err := ops.WriteRestore(a.dataDir(), mk); err != nil {
		return err
	}
	if bound == 0 {
		fmt.Fprintln(a.stdout, "no book is bound to a broker, so there was nothing to compare; the restore is marked reconciled on your word.")
	} else {
		fmt.Fprintf(a.stdout, "%d bound book(s) were synced with the broker after the restore and have no open difference; the restore is marked reconciled.\n", bound)
	}
	fmt.Fprintln(a.stdout, "New orders stay switched off until you enable them: order control enable -env ENV -reason TEXT -by NAME.")
	return nil
}

func opsDrill(ctx context.Context, a *app, args []string) error {
	if a.cfg.DatabaseURL != "" {
		return errors.New("ops drill works on SQLite files and does not yet drill a Postgres database; ops backup restores every dump into a scratch database to prove it, and ops verify does the same for any backup")
	}
	fs := newFlags("ops drill")
	work := fs.String("work", "", "a folder outside the data folder for the copies (default: a temporary one, removed afterwards)")
	keep := fs.Bool("keep", false, "keep the copies")
	asJSON := fs.Bool("json", false, "print JSON")
	t := ops.DefaultTargets()
	bt := fs.Duration("target-backup", t.Backup, "most a backup may take (0: not checked)")
	rt := fs.Duration("target-restore", t.Restore, "most a restore may take (0: not checked)")
	ot := fs.Duration("target-open", t.Open, "most opening and migrating a restored copy may take (0: not checked)")
	if rest, err := parseFlags(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	w := *work
	if w == "" {
		d, err := os.MkdirTemp("", "stoxicity-drill-")
		if err != nil {
			return err
		}
		w = d
		if !*keep {
			defer os.RemoveAll(d)
		}
	}
	size, _ := ops.DirSize(a.dataDir())
	if q, err := a.quota(); err == nil {
		if err := (ops.Quota{MinFree: q.MinFree}).Check(w, size*4); err != nil {
			return fmt.Errorf("no drill was run: %w", err)
		}
	}
	rep, err := ops.RunDrills(ctx, a.dataDir(), w, ops.Targets{Backup: *bt, Restore: *rt, Open: *ot})
	if err != nil {
		return err
	}
	if *asJSON {
		if err := printJSON(a, rep); err != nil {
			return err
		}
	} else {
		tb := cliTable{Header: []string{"drill", "result", "took", "what"}}
		for _, r := range rep.Results {
			res, st := "pass", cliGreen
			if !r.Passed {
				res, st = "FAIL", cliRed
			}
			tb.Rows = append(tb.Rows, []cliCell{plain(r.Name), styled(res, st), plain(r.Took.Round(time.Millisecond).String()), plain(r.Detail)})
		}
		tb.write(a.stdout, useColor(a.stdout))
		fmt.Fprintf(a.stdout, "\ndata folder %d MiB, %d database(s). Targets: backup %s, restore %s, open %s (illustrative until you set them).\n", rep.Data>>20, rep.DBs, *bt, *rt, *ot)
		fmt.Fprintln(a.stdout, "Run on copies: the live databases were only read. Timings are of this machine and this data size.")
		if *keep {
			fmt.Fprintf(a.stdout, "copies kept in %s\n", w)
		}
	}
	if !rep.Passed {
		return errReported
	}
	return nil
}

func opsService(ctx context.Context, a *app, args []string) error {
	fs := newFlags("ops service")
	out := fs.String("out", "", "write the files into this folder (nothing is installed or enabled)")
	mirror := fs.String("mirror", "", "a second backup location for the backup job")
	logs := fs.String("logs", "", "launchd: the folder for the job logs")
	envFile := fs.String("env-file", "", "systemd: the settings file to name as EnvironmentFile")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("usage: ops service launchd|systemd [-out DIR] [-mirror DIR] [-logs DIR] [-env-file FILE]")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cfg, _ := filepath.Abs(a.baseDir)
	spec := ops.ServiceSpec{Kind: rest[0], Binary: exe, ConfigDir: cfg, LogDir: *logs, Mirror: *mirror, EnvFile: *envFile}
	if rest[0] == "launchd" && spec.LogDir == "" {
		spec.LogDir = filepath.Join(cfg, "logs")
	}
	files, err := ops.Service(spec)
	if err != nil {
		return usageErrorf("%v", err)
	}
	for _, f := range files {
		if *out == "" {
			fmt.Fprintf(a.stdout, "# ---- %s ----\n%s\n", f.Name, f.Content)
			continue
		}
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return err
		}
		p := filepath.Join(*out, f.Name)
		fh, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("%s: %w (existing files are not replaced)", f.Name, err)
		}
		if _, err := fh.WriteString(f.Content); err != nil {
			fh.Close()
			return err
		}
		fh.Close()
		fmt.Fprintf(a.stdout, "wrote %s\n", p)
	}
	fmt.Fprintln(a.stdout, "Nothing was installed or started. Read the files, place them (launchd: ~/Library/LaunchAgents and launchctl load; systemd: ~/.config/systemd/user and systemctl --user enable --now the .timer files), and check with ops health.")
	return nil
}

func opsLeases(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 {
		return usageErrorf("ops leases takes no arguments")
	}
	ls, err := a.leases().List()
	if err != nil {
		return err
	}
	if len(ls) == 0 {
		fmt.Fprintln(a.stdout, "no job holds or has held a lease")
		return nil
	}
	t := cliTable{Header: []string{"job", "holder", "state", "until", "generation"}, Right: []bool{false, false, false, false, true}}
	for _, l := range ls {
		st, s := "expired", cliDim
		if l.Active {
			st, s = "held", cliYellow
		}
		t.Rows = append(t.Rows, []cliCell{plain(l.Name), plain(l.Holder), styled(st, s), plain(l.Expires.UTC().Format(time.RFC3339)), plain(strconv.FormatInt(l.Gen, 10))})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}
