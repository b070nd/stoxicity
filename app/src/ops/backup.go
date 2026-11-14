package ops

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the driver; the store registers the same one

	"github.com/b070nd/stoxicity/app/src/store"
)

// BackupFormat names the manifest layout. A change that removes or renames a
// field gets a new string.
const BackupFormat = "stoxicity.backup/1"

// FileEntry is one file of a backup.
type FileEntry struct {
	Path   string           `json:"path"` // relative, slash-separated
	Kind   string           `json:"kind"` // db or file
	SHA256 string           `json:"sha256"`
	Bytes  int64            `json:"bytes"`
	Schema int              `json:"schema,omitempty"` // migration applied, databases only
	Tables map[string]int64 `json:"tables,omitempty"` // rows per table ("schema.table" in a Postgres archive), databases only
	// Schemas is the newest migration applied in each schema of a Postgres archive.
	Schemas map[string]int `json:"schemas,omitempty"`
}

// Manifest describes a backup: every file with its checksum and, for a
// database, the schema it was at and the rows of every table. It travels with
// the backup and is copied into a data folder restored from it.
type Manifest struct {
	Format string    `json:"format"`
	ID     string    `json:"id"`
	Taken  time.Time `json:"taken"`
	// Coherent says no database changed while the set was copied: the files are
	// one moment of the whole data folder, not several.
	Coherent bool `json:"coherent"`
	Attempts int  `json:"attempts"`
	// Window is how long copying the databases took.
	WindowMillis int64                `json:"window_ms"`
	Known        store.SchemaVersions `json:"known_schema"`
	Files        []FileEntry          `json:"files"`
}

// Databases returns the database files of a data folder, relative to it.
func Databases(dataDir string) ([]string, error) {
	var out []string
	if _, err := os.Stat(filepath.Join(dataDir, "base.db")); err == nil {
		out = append(out, "base.db")
	}
	ents, err := os.ReadDir(filepath.Join(dataDir, "tenants"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".db") {
			out = append(out, "tenants/"+e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func roDB(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	return sql.Open("sqlite", u.String())
}

func tableCounts(ctx context.Context, path string) (map[string]int64, error) {
	db, err := roDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := map[string]int64{}
	for _, n := range names {
		var c int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+strings.ReplaceAll(n, `"`, `""`)+`"`).Scan(&c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, nil
}

func integrity(ctx context.Context, path string) error {
	db, err := roDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var r string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&r); err != nil {
		return err
	}
	if r != "ok" {
		return fmt.Errorf("integrity check failed: %s", r)
	}
	return nil
}

// vacuumInto writes a consistent snapshot of src to dst with SQLite's own
// mechanism. The source is opened read-only and is not written.
func vacuumInto(ctx context.Context, src, dst string) error {
	db, err := roDB(src)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(dst, `'`, `''`)+`'`); err != nil {
		return fmt.Errorf("snapshot of %s: %w", filepath.Base(src), err)
	}
	return nil
}

// fingerprint identifies the state of the databases: the size and time of each
// file and its write-ahead log, and the rows of every table. A commit changes
// at least one of them.
func fingerprint(ctx context.Context, dataDir string, dbs []string) (string, error) {
	h := sha256.New()
	for _, d := range dbs {
		p := filepath.Join(dataDir, filepath.FromSlash(d))
		// rows first: opening a database can touch its log, and what the open
		// itself changes must be there before the files are looked at
		c, err := tableCounts(ctx, p)
		if err != nil {
			return "", err
		}
		for _, f := range []string{p, p + "-wal"} {
			if st, err := os.Stat(f); err == nil {
				fmt.Fprintf(h, "%s %d %d\n", d, st.Size(), st.ModTime().UnixNano())
			}
		}
		names := make([]string, 0, len(c))
		for n := range c {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(h, "%s.%s=%d\n", d, n, c[n])
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sumFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}

var idRE = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

// Options change how a snapshot is taken.
type SnapshotOptions struct {
	Now func() time.Time
	// AllowIncoherent keeps a set that could not be copied in a quiet moment,
	// marked as such. Without it the snapshot fails.
	AllowIncoherent bool
	// Fault, when set, is called at named stages and may return an error to
	// simulate the program stopping there (used by the drills).
	Fault func(stage string) error
}

func (o SnapshotOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o SnapshotOptions) fault(stage string) error {
	if o.Fault != nil {
		return o.Fault(stage)
	}
	return nil
}

// Snapshot copies every database and the company files of dataDir into
// root/<UTC time>/ and writes the manifest. The databases are copied one after
// the other, then the data folder is looked at again: if any database changed
// in between, the set is not one moment and the copy is made again (three
// tries). The directory is built under a ".partial" name and renamed when
// complete, so a stop in the middle leaves nothing that looks like a backup.
func Snapshot(ctx context.Context, dataDir, root string, opt SnapshotOptions) (string, Manifest, error) {
	var m Manifest
	dbs, err := Databases(dataDir)
	if err != nil {
		return "", m, err
	}
	if len(dbs) == 0 {
		return "", m, errors.New("ops: the data folder holds no database")
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
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		os.RemoveAll(partial)
		if err := os.MkdirAll(partial, 0o755); err != nil {
			return "", m, err
		}
		start := time.Now()
		before, err := fingerprint(ctx, dataDir, dbs)
		if err != nil {
			os.RemoveAll(partial)
			return "", m, err
		}
		files := []FileEntry{}
		for _, d := range dbs {
			if err := ctx.Err(); err != nil {
				os.RemoveAll(partial)
				return "", m, err
			}
			if err := opt.fault("database:" + d); err != nil {
				return "", m, err // a stop: the .partial folder stays, as it would after a crash
			}
			dst := filepath.Join(partial, filepath.FromSlash(d))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return "", m, err
			}
			if err := vacuumInto(ctx, filepath.Join(dataDir, filepath.FromSlash(d)), dst); err != nil {
				os.RemoveAll(partial)
				return "", m, err
			}
			if err := integrity(ctx, dst); err != nil {
				os.RemoveAll(partial)
				return "", m, fmt.Errorf("ops: the copy of %s: %w", d, err)
			}
			files = append(files, FileEntry{Path: d, Kind: "db"})
		}
		window := time.Since(start)
		after, err := fingerprint(ctx, dataDir, dbs)
		if err != nil {
			os.RemoveAll(partial)
			return "", m, err
		}
		coherent := before == after
		if !coherent && attempt < 3 {
			lastErr = errors.New("ops: a database changed while the set was being copied")
			continue
		}
		if !coherent && !opt.AllowIncoherent {
			os.RemoveAll(partial)
			return "", m, errors.New("ops: the databases kept changing while they were copied, so the copies would not be one moment; stop the other jobs and try again")
		}
		if err := opt.fault("company"); err != nil {
			return "", m, err
		}
		if st, err := os.Stat(filepath.Join(dataDir, "company")); err == nil && st.IsDir() {
			if err := copyTree(ctx, filepath.Join(dataDir, "company"), filepath.Join(partial, "company")); err != nil {
				os.RemoveAll(partial)
				return "", m, err
			}
		}
		// the entries, their checksums and the rows of every table of every copy
		for i := range files {
			p := filepath.Join(partial, filepath.FromSlash(files[i].Path))
			tabs, err := tableCounts(ctx, p)
			if err != nil {
				os.RemoveAll(partial)
				return "", m, err
			}
			files[i].Tables = tabs
			sd, err := store.ReadSchema(ctx, p)
			if err != nil {
				os.RemoveAll(partial)
				return "", m, err
			}
			files[i].Schema = sd.Applied
		}
		walkErr := filepath.WalkDir(filepath.Join(partial, "company"), func(p string, d fs.DirEntry, err error) error {
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
		if walkErr != nil {
			os.RemoveAll(partial)
			return "", m, walkErr
		}
		for i := range files {
			sum, n, err := sumFile(filepath.Join(partial, filepath.FromSlash(files[i].Path)))
			if err != nil {
				os.RemoveAll(partial)
				return "", m, err
			}
			files[i].SHA256, files[i].Bytes = sum, n
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		m = Manifest{Format: BackupFormat, ID: id, Taken: opt.now(), Coherent: coherent, Attempts: attempt, WindowMillis: window.Milliseconds(), Known: known, Files: files}
		raw, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(filepath.Join(partial, "MANIFEST.json"), append(raw, '\n'), 0o644); err != nil {
			return "", m, err
		}
		if err := writeLegacy(partial, files); err != nil {
			return "", m, err
		}
		if err := opt.fault("rename"); err != nil {
			return "", m, err
		}
		if err := os.Rename(partial, dest); err != nil {
			return "", m, err
		}
		return dest, m, nil
	}
	return "", m, lastErr
}

// writeLegacy writes the files backup.sh reads (MANIFEST and the .counts of
// each database), so the shell tool can verify and restore a backup made here.
func writeLegacy(dir string, files []FileEntry) error {
	var sums strings.Builder
	for _, f := range files {
		if f.Kind == "db" {
			names := make([]string, 0, len(f.Tables))
			for n := range f.Tables {
				names = append(names, n)
			}
			sort.Strings(names)
			var b strings.Builder
			for _, n := range names {
				fmt.Fprintf(&b, "%s\t%d\n", n, f.Tables[n])
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(f.Path)+".counts"), []byte(b.String()), 0o644); err != nil {
				return err
			}
		}
	}
	var all []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && d.Name() != "MANIFEST" {
			rel, _ := filepath.Rel(dir, p)
			all = append(all, "./"+filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(all)
	for _, rel := range all {
		sum, _, err := sumFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  %s\n", sum, rel)
	}
	return os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte(sums.String()), 0o644)
}

// ReadManifest reads the manifest of a backup folder.
func ReadManifest(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "MANIFEST.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, fmt.Errorf("ops: %s has no MANIFEST.json (a backup made by backup.sh is checked with ./backup.sh verify)", filepath.Base(dir))
	}
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("ops: the manifest of %s is damaged: %w", filepath.Base(dir), err)
	}
	if m.Format != BackupFormat {
		return Manifest{}, fmt.Errorf("ops: %s is a %q backup and this program reads %q", filepath.Base(dir), m.Format, BackupFormat)
	}
	return m, nil
}

// VerifyReport says whether a backup can be trusted and what is wrong.
type VerifyReport struct {
	ID       string   `json:"id"`
	OK       bool     `json:"ok"`
	Coherent bool     `json:"coherent"`
	Files    int      `json:"files"`
	Bytes    int64    `json:"bytes"`
	Problems []string `json:"problems"`
	// Notes are checks that could not be made; they are not problems.
	Notes []string      `json:"notes,omitempty"`
	Took  time.Duration `json:"took_ns"`
}

// VerifyOptions say how far a verification may go.
type VerifyOptions struct {
	// PostgresURL is a server to restore a Postgres archive into (a scratch
	// database that is dropped afterwards) to count its rows. Without it an
	// archive is checked by checksum and by listing only.
	PostgresURL string
}

// Verify checks a backup against its manifest: every file is there and has
// its checksum, no file is unlisted, every database passes SQLite's integrity
// check and has the rows the manifest says, and none was written by a newer
// program than this one.
func Verify(ctx context.Context, dir string) (VerifyReport, error) {
	return VerifyWith(ctx, dir, VerifyOptions{})
}

// VerifyWith is Verify with options.
func VerifyWith(ctx context.Context, dir string, o VerifyOptions) (VerifyReport, error) {
	start := time.Now()
	rep := VerifyReport{Problems: []string{}}
	m, err := ReadManifest(dir)
	if err != nil {
		return rep, err
	}
	rep.ID, rep.Coherent = m.ID, m.Coherent
	listed := map[string]bool{"MANIFEST.json": true, "MANIFEST": true}
	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		listed[f.Path] = true
		if f.Kind == "db" {
			listed[f.Path+".counts"] = true
		}
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		sum, n, err := sumFile(p)
		if err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s: %v", f.Path, errors.Unwrap(err)))
			continue
		}
		rep.Files++
		rep.Bytes += n
		if sum != f.SHA256 || n != f.Bytes {
			rep.Problems = append(rep.Problems, f.Path+": the file differs from its checksum")
			continue
		}
		if f.Kind == pgDumpKind {
			cur, _ := store.KnownSchema()
			verifyPGDump(ctx, p, f, o, &rep, cur)
			continue
		}
		if f.Kind != "db" {
			continue
		}
		if err := integrity(ctx, p); err != nil {
			rep.Problems = append(rep.Problems, f.Path+": "+err.Error())
			continue
		}
		got, err := tableCounts(ctx, p)
		if err != nil {
			rep.Problems = append(rep.Problems, f.Path+": "+err.Error())
			continue
		}
		if !sameCounts(got, f.Tables) {
			rep.Problems = append(rep.Problems, f.Path+": the rows of its tables differ from those recorded at backup")
		}
		limit := m.Known.Tenant
		if f.Path == "base.db" {
			limit = m.Known.Base
		}
		if cur, err := store.KnownSchema(); err == nil {
			limit = cur.Tenant
			if f.Path == "base.db" {
				limit = cur.Base
			}
		}
		if f.Schema > limit {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s: written by a newer program (schema %d; this program knows %d)", f.Path, f.Schema, limit))
		}
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			if !listed[rel] {
				rep.Problems = append(rep.Problems, rel+": not in the manifest")
			}
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	sort.Strings(rep.Problems)
	rep.OK = len(rep.Problems) == 0
	rep.Took = time.Since(start)
	return rep, nil
}

func sameCounts(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// List returns the backups under root, newest first. A ".partial" folder is not
// a backup and is not listed.
func List(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range ents {
		if e.IsDir() && idRE.MatchString(e.Name()) {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "MANIFEST.json")); err == nil {
				ids = append(ids, e.Name())
			}
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

// Rotate removes old backups under root. It keeps the newest keepLast and, for
// each of the newest keepWeeks calendar weeks, the newest backup of that week.
// A backup named in protect is never removed, and neither is the newest one.
// Only folders named like a backup that hold a manifest are touched. It
// returns what it removed.
func Rotate(root string, keepLast, keepWeeks int, protect ...string) ([]string, error) {
	if keepLast < 1 {
		return nil, errors.New("ops: keep at least one backup")
	}
	ids, err := List(root)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, p := range protect {
		keep[p] = true
	}
	for i, id := range ids {
		if i < keepLast || i == 0 {
			keep[id] = true
		}
	}
	weeks := map[string]bool{}
	var order []string
	for _, id := range ids { // newest first
		t, err := time.Parse("20060102T150405Z", id)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		key := fmt.Sprintf("%d-W%02d", y, w)
		if !weeks[key] {
			weeks[key] = true
			order = append(order, key)
			if len(order) <= keepWeeks {
				keep[id] = true
			}
		}
	}
	var removed []string
	for _, id := range ids {
		if keep[id] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, id)); err != nil {
			return removed, err
		}
		removed = append(removed, id)
	}
	return removed, nil
}

// MirrorResult says where a backup was copied and how independent the copy is.
type MirrorResult struct {
	Path       string
	SameDevice bool
	Report     VerifyReport
}

// Mirror copies a backup folder to another root and verifies the copy there.
// If the other root is on the same filesystem the copy guards against deletion
// and not against the disk failing, and the result says so.
func Mirror(ctx context.Context, backupDir, mirrorRoot string) (MirrorResult, error) {
	var res MirrorResult
	id := filepath.Base(backupDir)
	if !idRE.MatchString(id) {
		return res, fmt.Errorf("ops: %s is not named like a backup", id)
	}
	if err := os.MkdirAll(mirrorRoot, 0o755); err != nil {
		return res, err
	}
	dest := filepath.Join(mirrorRoot, id)
	if _, err := os.Stat(dest); err == nil {
		return res, fmt.Errorf("ops: %s already exists in the mirror", id)
	}
	partial := dest + ".partial"
	os.RemoveAll(partial)
	if err := copyTree(ctx, backupDir, partial); err != nil {
		os.RemoveAll(partial)
		return res, err
	}
	if same, err := SameDevice(backupDir, mirrorRoot); err == nil {
		res.SameDevice = same
	}
	rep, err := Verify(ctx, partial)
	if err != nil {
		os.RemoveAll(partial)
		return res, err
	}
	res.Report = rep
	if !rep.OK {
		os.RemoveAll(partial)
		return res, fmt.Errorf("ops: the copy in the mirror does not verify: %s", strings.Join(rep.Problems, "; "))
	}
	if err := os.Rename(partial, dest); err != nil {
		return res, err
	}
	res.Path = dest
	return res, nil
}
