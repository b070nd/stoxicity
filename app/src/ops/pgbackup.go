package ops

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/b070nd/stoxicity/app/src/store"
)

// A Postgres installation is backed up with pg_dump and checked by restoring
// the dump into a scratch database and counting every table, which is the only
// way to know a dump can be restored. The dump is one custom-format archive of
// the whole database (the base schema and every tenant's), taken in a single
// transaction, so it is always one moment of the data. The program never writes
// a dump over anything: a restore creates a new database beside the old one.

// PGDumpFile is the name of the archive inside a backup folder.
const PGDumpFile = "postgres.dump"

// pgDumpKind is the FileEntry kind of the archive.
const pgDumpKind = "pgdump"

// ErrNoPGTool says pg_dump or pg_restore is missing.
var ErrNoPGTool = errors.New("ops: pg_dump and pg_restore are needed (install the PostgreSQL client tools, of the server's version or newer)")

func pgTool(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/lib/postgresql/18/bin", "/usr/lib/postgresql/17/bin", "/usr/lib/postgresql/16/bin"} {
			cand := filepath.Join(dir, name)
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand, nil
			}
		}
		return "", fmt.Errorf("%w (%s not found)", ErrNoPGTool, name)
	}
	return p, nil
}

// pgConn splits a database URL into what to pass on a command line and the
// environment that carries the password, so the password never appears in the
// process list.
func pgConn(raw string) (dsn string, env []string, err error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return raw, nil, nil // a key/value connection string is passed on as it is
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
		}
	}
	return u.String(), env, nil
}

func withDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("ops: the database URL must be a postgresql:// URL")
	}
	u.Path = "/" + name
	return u.String(), nil
}

func dbName(raw string) (string, error) {
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		return "", fmt.Errorf("ops: database URL: %w", err)
	}
	return cfg.Database, nil
}

func run(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 600 {
			msg = msg[:600] + "..."
		}
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, msg)
	}
	return nil
}

// scratchDB is a database made to hold one restore. It is created with the
// byte-order locale the program requires, so a restored copy opens like the
// original.
type scratchDB struct {
	URL   string
	admin string
	name  string
}

func newScratchDB(ctx context.Context, base, prefix string) (*scratchDB, error) {
	var b [5]byte
	rand.Read(b[:])
	name := prefix + hex.EncodeToString(b[:])
	admin, err := withDatabase(base, "postgres")
	if err != nil {
		return nil, err
	}
	if err := createDB(ctx, admin, name); err != nil {
		return nil, err
	}
	u, _ := withDatabase(base, name)
	return &scratchDB{URL: u, admin: admin, name: name}, nil
}

func createDB(ctx context.Context, admin, name string) error {
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return fmt.Errorf("ops: cannot connect to create a database: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()+
		` TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER builtin BUILTIN_LOCALE 'C.UTF-8'`); err != nil {
		return fmt.Errorf("ops: cannot create database %s (the role needs CREATEDB): %w", name, err)
	}
	return nil
}

// drop removes the database. Right after a restore the server's autovacuum
// works on the new tables, and a role that is not a superuser cannot terminate
// its workers, so the drop is retried for a while until they are done.
func (s *scratchDB) drop() error {
	var last error
	for i := 0; i < 60; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, err := pgx.Connect(ctx, s.admin)
		if err == nil {
			_, err = conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{s.name}.Sanitize()+` WITH (FORCE)`)
			conn.Close(ctx)
		}
		cancel()
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(time.Second)
	}
	return fmt.Errorf("ops: the scratch database %s could not be dropped (drop it by hand): %w", s.name, last)
}

func pgRestoreInto(ctx context.Context, dump, target string) error {
	tool, err := pgTool("pg_restore")
	if err != nil {
		return err
	}
	dsn, env, _ := pgConn(target)
	return run(ctx, env, tool, "--no-owner", "--no-acl", "--exit-on-error", "--dbname", dsn, dump)
}

func pgList(ctx context.Context, dump string) error {
	tool, err := pgTool("pg_restore")
	if err != nil {
		return err
	}
	return run(ctx, nil, tool, "--list", dump)
}

// SnapshotPostgres writes a backup of the Postgres database at pgURL (and the
// company files of dataDir) into root/<UTC time>/, with a manifest. The dump
// is restored into a scratch database and every table counted; those counts go
// into the manifest, and when the live database did not change during the dump
// they must equal the live counts, or the backup fails. The folder is built
// under a ".partial" name and renamed when complete.
func SnapshotPostgres(ctx context.Context, pgURL, dataDir, root string, opt SnapshotOptions) (string, Manifest, error) {
	var m Manifest
	dump, err := pgTool("pg_dump")
	if err != nil {
		return "", m, err
	}
	known, err := store.KnownSchema()
	if err != nil {
		return "", m, err
	}
	id := opt.now().Format("20060102T150405Z")
	dest, partial := filepath.Join(root, id), filepath.Join(root, id+".partial")
	if _, err := os.Stat(dest); err == nil {
		return "", m, fmt.Errorf("ops: backup %s exists", id)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", m, err
	}
	os.RemoveAll(partial)
	if err := os.MkdirAll(partial, 0o755); err != nil {
		return "", m, err
	}
	fail := func(err error) (string, Manifest, error) {
		os.RemoveAll(partial)
		return "", m, err
	}
	start := time.Now()
	before, err := store.InspectPostgres(ctx, pgURL, true)
	if err != nil {
		return fail(err)
	}
	if len(before.Schemas) == 0 {
		return fail(errors.New("ops: the database holds no data of this program"))
	}
	if err := opt.fault("database:" + PGDumpFile); err != nil {
		return "", m, err
	}
	dsn, env, _ := pgConn(pgURL)
	out := filepath.Join(partial, PGDumpFile)
	if err := run(ctx, env, dump, "--format=custom", "--no-owner", "--no-acl", "--file", out, "--dbname", dsn); err != nil {
		return fail(err)
	}
	window := time.Since(start)
	after, err := store.InspectPostgres(ctx, pgURL, true)
	if err != nil {
		return fail(err)
	}
	// What the archive holds is what it restores to.
	scratch, err := newScratchDB(ctx, pgURL, "stoxicity_verify_")
	if err != nil {
		return fail(err)
	}
	defer scratch.drop()
	if err := pgRestoreInto(ctx, out, scratch.URL); err != nil {
		return fail(fmt.Errorf("ops: the dump does not restore: %w", err))
	}
	got, err := store.InspectPostgres(ctx, scratch.URL, true)
	if err != nil {
		return fail(err)
	}
	if sameCounts(before.Tables, after.Tables) && !sameCounts(got.Tables, before.Tables) {
		return fail(errors.New("ops: the restored dump has different rows from the live database, which did not change meanwhile; the backup was not kept"))
	}
	if err := opt.fault("company"); err != nil {
		return "", m, err
	}
	if st, err := os.Stat(filepath.Join(dataDir, "company")); err == nil && st.IsDir() {
		if err := copyTree(ctx, filepath.Join(dataDir, "company"), filepath.Join(partial, "company")); err != nil {
			return fail(err)
		}
	}
	files := []FileEntry{{Path: PGDumpFile, Kind: pgDumpKind, Tables: got.Tables, Schemas: got.Schemas}}
	werr := filepath.WalkDir(filepath.Join(partial, "company"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(partial, p)
			files = append(files, FileEntry{Path: filepath.ToSlash(rel), Kind: "file"})
		}
		return nil
	})
	if werr != nil {
		return fail(werr)
	}
	for i := range files {
		sum, n, err := sumFile(filepath.Join(partial, filepath.FromSlash(files[i].Path)))
		if err != nil {
			return fail(err)
		}
		files[i].SHA256, files[i].Bytes = sum, n
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	m = Manifest{Format: BackupFormat, ID: id, Taken: opt.now(), Coherent: true, Attempts: 1, WindowMillis: window.Milliseconds(), Known: known, Files: files}
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(partial, "MANIFEST.json"), append(raw, '\n'), 0o644); err != nil {
		return fail(err)
	}
	if err := writeLegacy(partial, files); err != nil {
		return fail(err)
	}
	if err := opt.fault("rename"); err != nil {
		return "", m, err
	}
	if err := os.Rename(partial, dest); err != nil {
		return fail(err)
	}
	return dest, m, nil
}

// verifyPGDump checks the archive of a backup: it must list, and, when a
// Postgres URL is given to restore into, restore to exactly the rows the
// manifest records. Without a URL the rows are not checked and the report says so.
func verifyPGDump(ctx context.Context, path string, f FileEntry, o VerifyOptions, rep *VerifyReport, known store.SchemaVersions) {
	if err := pgList(ctx, path); err != nil {
		if errors.Is(err, ErrNoPGTool) {
			rep.Notes = append(rep.Notes, f.Path+": the archive was not listed ("+err.Error()+")")
		} else {
			rep.Problems = append(rep.Problems, f.Path+": "+err.Error())
		}
		return
	}
	for schema, applied := range f.Schemas {
		if applied > known.KnownFor(schema) {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s: schema %s was written by a newer program (schema %d; this program knows %d)", f.Path, schema, applied, known.KnownFor(schema)))
		}
	}
	if o.PostgresURL == "" {
		rep.Notes = append(rep.Notes, f.Path+": the rows were not checked (no Postgres to restore into is configured)")
		return
	}
	scratch, err := newScratchDB(ctx, o.PostgresURL, "stoxicity_verify_")
	if err != nil {
		rep.Problems = append(rep.Problems, f.Path+": "+err.Error())
		return
	}
	defer func() {
		if err := scratch.drop(); err != nil {
			rep.Notes = append(rep.Notes, err.Error())
		}
	}()
	if err := pgRestoreInto(ctx, path, scratch.URL); err != nil {
		rep.Problems = append(rep.Problems, f.Path+": does not restore: "+err.Error())
		return
	}
	got, err := store.InspectPostgres(ctx, scratch.URL, true)
	if err != nil {
		rep.Problems = append(rep.Problems, f.Path+": "+err.Error())
		return
	}
	if !sameCounts(got.Tables, f.Tables) {
		rep.Problems = append(rep.Problems, f.Path+": the rows of its tables differ from those recorded at backup")
	}
}

// PGRestoreResult says what RestorePostgres made.
type PGRestoreResult struct {
	BackupID string
	Database string // the new database
	URL      string // its URL: set STOXICITY_DATABASE_URL to it to use it
	Previous string // the database that was configured, left untouched
	Tables   int
	Rows     int64
	Total    time.Duration
}

// RestorePostgres restores a backup into a new database on the server of
// pgURL, named after the configured one with a time stamp. The configured
// database is not touched: switching to the restored one is the owner's step.
// The backup is checked before and the restored database is counted against the
// manifest after; on any difference the new database is dropped.
func RestorePostgres(ctx context.Context, backupDir, pgURL string, opt RestoreOptions) (PGRestoreResult, error) {
	var res PGRestoreResult
	t0 := time.Now()
	m, err := ReadManifest(backupDir)
	if err != nil {
		return res, err
	}
	res.BackupID = m.ID
	var entry *FileEntry
	for i := range m.Files {
		if m.Files[i].Kind == pgDumpKind {
			entry = &m.Files[i]
		}
	}
	if entry == nil {
		return res, fmt.Errorf("ops: backup %s holds no Postgres archive (it was made from SQLite files; restore it with the SQLite settings)", m.ID)
	}
	rep, err := Verify(ctx, backupDir)
	if err != nil {
		return res, err
	}
	if !rep.OK {
		return res, fmt.Errorf("ops: backup %s does not verify, nothing was changed: %s", m.ID, strings.Join(rep.Problems, "; "))
	}
	cur, err := dbName(pgURL)
	if err != nil {
		return res, err
	}
	res.Previous = cur
	name := cur + "_restored_" + opt.now().Format("20060102t150405z")
	name = strings.ToLower(name)
	admin, err := withDatabase(pgURL, "postgres")
	if err != nil {
		return res, err
	}
	if err := opt.fault("verified"); err != nil {
		return res, err
	}
	if err := createDB(ctx, admin, name); err != nil {
		return res, err
	}
	target, _ := withDatabase(pgURL, name)
	drop := &scratchDB{admin: admin, name: name}
	if err := pgRestoreInto(ctx, filepath.Join(backupDir, PGDumpFile), target); err != nil {
		drop.drop()
		return res, fmt.Errorf("ops: the restore failed and the new database was dropped: %w", err)
	}
	got, err := store.InspectPostgres(ctx, target, true)
	if err != nil {
		drop.drop()
		return res, err
	}
	if !sameCounts(got.Tables, entry.Tables) {
		drop.drop()
		return res, errors.New("ops: the restored database has different rows from the backup; it was dropped")
	}
	res.Database, res.URL, res.Tables = name, target, len(got.Tables)
	for _, n := range got.Tables {
		res.Rows += n
	}
	res.Total = time.Since(t0)
	return res, nil
}
