package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RestoreFile is the marker a restore leaves in the data folder. While its
// State is "needs_reconcile" the program refuses new orders: what a backup
// holds is how things were, and the broker may have moved since.
const RestoreFile = "restore.json"

// Restore marker states.
const (
	NeedsReconcile = "needs_reconcile"
	Reconciled     = "reconciled"
)

// RestoreMarker is the content of restore.json.
type RestoreMarker struct {
	State      string    `json:"state"`
	Restored   time.Time `json:"restored"`
	BackupID   string    `json:"backup"`
	BackupTook time.Time `json:"backup_taken"`
	// Previous is the name of the folder the restore moved aside.
	Previous string `json:"previous_folder,omitempty"`
	// ClearedBy and Cleared are set when the broker has been reconciled.
	ClearedBy string    `json:"cleared_by,omitempty"`
	Cleared   time.Time `json:"cleared,omitempty"`
	Note      string    `json:"note,omitempty"`
}

// ReadRestore reads the marker; ok is false when the folder was never restored.
func ReadRestore(dataDir string) (RestoreMarker, bool, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, RestoreFile))
	if errors.Is(err, fs.ErrNotExist) {
		return RestoreMarker{}, false, nil
	}
	if err != nil {
		return RestoreMarker{}, false, err
	}
	var m RestoreMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return RestoreMarker{}, false, fmt.Errorf("ops: %s is damaged: %w", RestoreFile, err)
	}
	return m, true, nil
}

// WriteRestore replaces the marker.
func WriteRestore(dataDir string, m RestoreMarker) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := filepath.Join(dataDir, RestoreFile+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dataDir, RestoreFile))
}

// Suspension is the reason new orders are refused because of a restore, or ""
// when nothing is pending. An unreadable marker also suspends: not knowing is
// not a reason to send orders.
func Suspension(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	m, ok, err := ReadRestore(dataDir)
	if err != nil {
		return "restore: the restore marker cannot be read (" + err.Error() + ")"
	}
	if ok && m.State == NeedsReconcile {
		return fmt.Sprintf("restore: this data folder was restored from backup %s on %s and has not been reconciled with the broker (run ops reconciled)", m.BackupID, m.Restored.UTC().Format("2006-01-02"))
	}
	return ""
}

// Running lists the processes of this program, other than the caller, that
// look like it is running. pgrep may be missing; then it says it cannot tell.
func Running() (pids []int, known bool) {
	out, err := exec.Command("pgrep", "-x", "stoxicity|stoxicity-tui").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, true // pgrep ran and found none
		}
		return nil, false
	}
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(f); err == nil && n != os.Getpid() {
			pids = append(pids, n)
		}
	}
	return pids, true
}

// inUse reports whether another connection holds a database open for writing
// right now.
func inUse(ctx context.Context, path string) bool {
	abs, _ := filepath.Abs(path)
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "_pragma=busy_timeout(0)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return true
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return true
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return true
	}
	conn.ExecContext(ctx, `ROLLBACK`)
	return false
}

// RestoreOptions change a restore.
type RestoreOptions struct {
	Now func() time.Time
	// Fault may return an error at a named stage to simulate a stop there.
	Fault func(stage string) error
	// SkipProcessCheck is for tests, which cannot stop the test binary.
	SkipProcessCheck bool
}

func (o RestoreOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o RestoreOptions) fault(stage string) error {
	if o.Fault != nil {
		return o.Fault(stage)
	}
	return nil
}

// RestoreResult says what a restore did and how long each part took.
type RestoreResult struct {
	BackupID string
	Previous string // name of the folder moved aside
	Verify   VerifyReport
	Stage    time.Duration
	Swap     time.Duration
	Total    time.Duration
	Files    int
	Bytes    int64
}

// Restore puts a backup in place of the data folder:
//
//  1. an earlier restore that stopped half way is finished or undone first;
//  2. the backup is verified in full;
//  3. the program must not be running: no process of its own, no unexpired
//     lease, no database held open for writing;
//  4. the backup is copied into a staging folder next to the data folder and
//     verified again there, with the restore marker already inside it;
//  5. the data folder is renamed aside (nothing is deleted) and the staging
//     folder takes its place.
//
// A stop at any point before step 5 leaves the data folder untouched. A stop
// between the two renames of step 5 leaves the data folder missing, and the
// next Restore or Recover puts one back.
func Restore(ctx context.Context, backupDir, dataDir string, opt RestoreOptions) (RestoreResult, error) {
	var res RestoreResult
	t0 := time.Now()
	dataDir = filepath.Clean(dataDir)
	if err := Recover(dataDir); err != nil {
		return res, err
	}
	m, err := ReadManifest(backupDir)
	if err != nil {
		return res, err
	}
	res.BackupID = m.ID
	rep, err := Verify(ctx, backupDir)
	res.Verify = rep
	if err != nil {
		return res, err
	}
	if !rep.OK {
		return res, fmt.Errorf("ops: backup %s does not verify, nothing was changed: %s", m.ID, strings.Join(rep.Problems, "; "))
	}
	if err := opt.fault("verified"); err != nil {
		return res, err
	}
	if !opt.SkipProcessCheck {
		pids, known := Running()
		if !known {
			return res, errors.New("ops: cannot tell whether the program is running (pgrep is missing); stop it and restore with ./backup.sh restore")
		}
		if len(pids) > 0 {
			return res, fmt.Errorf("ops: the program is running (process %v); stop it first", pids)
		}
	}
	if infos, err := (Leases{Dir: filepath.Join(dataDir, "leases"), Now: opt.Now}).List(); err == nil {
		for _, l := range infos {
			if l.Active {
				return res, fmt.Errorf("ops: the job %q holds a lease until %s; wait for it or stop it", l.Name, l.Expires.UTC().Format(time.RFC3339))
			}
		}
	}
	if dbs, err := Databases(dataDir); err == nil {
		for _, d := range dbs {
			if inUse(ctx, filepath.Join(dataDir, filepath.FromSlash(d))) {
				return res, fmt.Errorf("ops: %s is being written by another process; stop the program first", d)
			}
		}
	}

	stamp := opt.now().Format("20060102T150405Z")
	staging := dataDir + ".restore-" + stamp
	aside := dataDir + ".before-restore-" + stamp
	for _, p := range []string{staging, aside} {
		if _, err := os.Lstat(p); err == nil {
			return res, fmt.Errorf("ops: %s exists", filepath.Base(p))
		}
	}
	s0 := time.Now()
	fail := func(err error) (RestoreResult, error) {
		os.RemoveAll(staging)
		return res, err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return res, err
	}
	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := copyFile(filepath.Join(backupDir, filepath.FromSlash(f.Path)), filepath.Join(staging, filepath.FromSlash(f.Path))); err != nil {
			return fail(err)
		}
		res.Files++
		res.Bytes += f.Bytes
	}
	// the manifest travels with the data, so what this folder came from is known
	origin := filepath.Join(staging, "restored-from")
	for _, n := range []string{"MANIFEST.json", "MANIFEST"} {
		if err := copyFile(filepath.Join(backupDir, n), filepath.Join(origin, n)); err != nil {
			return fail(err)
		}
	}
	// every restored database is checked again where it now lives
	for _, f := range m.Files {
		if f.Kind != "db" {
			continue
		}
		p := filepath.Join(staging, filepath.FromSlash(f.Path))
		if err := integrity(ctx, p); err != nil {
			return fail(fmt.Errorf("ops: restored %s: %w", f.Path, err))
		}
		got, err := tableCounts(ctx, p)
		if err != nil {
			return fail(err)
		}
		if !sameCounts(got, f.Tables) {
			return fail(fmt.Errorf("ops: restored %s has different rows from the backup", f.Path))
		}
	}
	prev := ""
	if _, err := os.Stat(dataDir); err == nil {
		prev = filepath.Base(aside)
	}
	if err := WriteRestore(staging, RestoreMarker{State: NeedsReconcile, Restored: opt.now(), BackupID: m.ID, BackupTook: m.Taken, Previous: prev}); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(staging, ".staged"), []byte(m.ID+"\n"), 0o644); err != nil {
		return fail(err)
	}
	res.Stage = time.Since(s0)
	if err := opt.fault("staged"); err != nil {
		return res, err
	}

	w0 := time.Now()
	if prev != "" {
		if err := os.Rename(dataDir, aside); err != nil {
			return fail(err)
		}
	}
	if err := opt.fault("moved-aside"); err != nil {
		return res, err
	}
	if err := os.Rename(staging, dataDir); err != nil {
		if prev != "" {
			os.Rename(aside, dataDir)
		}
		return fail(err)
	}
	os.Remove(filepath.Join(dataDir, ".staged"))
	res.Swap = time.Since(w0)
	res.Previous = prev
	res.Total = time.Since(t0)
	return res, nil
}

// Recover finishes or undoes a restore that stopped between its two renames,
// when the data folder is missing. It does nothing when the data folder is
// there. A complete staged folder is moved into place; otherwise the folder
// that was moved aside is moved back. Nothing is deleted.
func Recover(dataDir string) error {
	if _, err := os.Stat(dataDir); err == nil {
		return nil
	}
	dir, base := filepath.Dir(dataDir), filepath.Base(dataDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var staged, asides []string
	for _, e := range ents {
		n := e.Name()
		switch {
		case strings.HasPrefix(n, base+".restore-"):
			if _, err := os.Stat(filepath.Join(dir, n, ".staged")); err == nil {
				staged = append(staged, n)
			}
		case strings.HasPrefix(n, base+".before-restore-"):
			asides = append(asides, n)
		}
	}
	sort.Strings(staged)
	sort.Strings(asides)
	switch {
	case len(staged) > 0:
		n := staged[len(staged)-1]
		if err := os.Rename(filepath.Join(dir, n), dataDir); err != nil {
			return err
		}
		os.Remove(filepath.Join(dataDir, ".staged"))
		return nil
	case len(asides) > 0:
		return os.Rename(filepath.Join(dir, asides[len(asides)-1]), dataDir)
	}
	return nil
}
