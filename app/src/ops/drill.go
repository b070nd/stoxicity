package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

// Targets are what a drill is measured against. They are starting points to be
// decided by the owner, not guarantees: a zero field is not checked.
type Targets struct {
	Backup  time.Duration // most a coherent backup may take
	Restore time.Duration // most a restore into an empty folder may take (the time to recover)
	Open    time.Duration // most opening and migrating a restored copy may take
}

// DefaultTargets are illustrative.
func DefaultTargets() Targets {
	return Targets{Backup: 5 * time.Minute, Restore: 15 * time.Minute, Open: 5 * time.Minute}
}

// DrillResult is one drill.
type DrillResult struct {
	Name     string             `json:"name"`
	Passed   bool               `json:"passed"`
	Took     time.Duration      `json:"took_ns"`
	Detail   string             `json:"detail"`
	Measures map[string]float64 `json:"measures"`
}

// DrillReport is the result of all drills.
type DrillReport struct {
	Started time.Time     `json:"started"`
	Passed  bool          `json:"passed"`
	Data    uint64        `json:"data_bytes"`
	DBs     int           `json:"databases"`
	Targets Targets       `json:"targets"`
	Results []DrillResult `json:"results"`
}

// RunDrills runs the recovery drills on copies in work, which must be outside
// the data folder. The live databases are only read (snapshotted through
// read-only connections); every drill that writes does so in work. Each drill
// reports what it measured and whether it passed, and a failed drill does not
// stop the rest.
func RunDrills(ctx context.Context, dataDir, work string, t Targets) (DrillReport, error) {
	rep := DrillReport{Started: time.Now().UTC(), Targets: t, Passed: true}
	absData, _ := filepath.Abs(dataDir)
	absWork, _ := filepath.Abs(work)
	if rel, err := filepath.Rel(absData, absWork); err == nil && !strings.HasPrefix(rel, "..") {
		return rep, errors.New("ops: the drill folder must be outside the data folder")
	}
	dbs, err := Databases(dataDir)
	if err != nil {
		return rep, err
	}
	if len(dbs) == 0 {
		return rep, errors.New("ops: the data folder holds no database to drill on")
	}
	rep.DBs = len(dbs)
	rep.Data, _ = DirSize(dataDir)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return rep, err
	}
	record := func(r DrillResult) {
		if !r.Passed {
			rep.Passed = false
		}
		rep.Results = append(rep.Results, r)
	}
	run := func(name string, f func() (string, map[string]float64, error)) {
		t0 := time.Now()
		detail, ms, err := f()
		r := DrillResult{Name: name, Took: time.Since(t0), Detail: detail, Measures: ms, Passed: err == nil}
		if err != nil {
			r.Detail = strings.TrimSpace(detail + " " + err.Error())
		}
		if r.Measures == nil {
			r.Measures = map[string]float64{}
		}
		record(r)
	}
	within := func(what string, took, limit time.Duration) error {
		if limit > 0 && took > limit {
			return fmt.Errorf("%s took %s, over the target of %s", what, took.Round(time.Millisecond), limit)
		}
		return nil
	}

	backups := filepath.Join(work, "backups")
	var backup string
	var man Manifest
	run("backup", func() (string, map[string]float64, error) {
		t0 := time.Now()
		dir, m, err := Snapshot(ctx, dataDir, backups, SnapshotOptions{})
		took := time.Since(t0)
		if err != nil {
			return "", nil, err
		}
		backup, man = dir, m
		var bytes int64
		for _, f := range m.Files {
			bytes += f.Bytes
		}
		ms := map[string]float64{"seconds": took.Seconds(), "bytes": float64(bytes), "files": float64(len(m.Files)), "copy_window_ms": float64(m.WindowMillis), "attempts": float64(m.Attempts)}
		if !m.Coherent {
			return "the databases changed while they were copied", ms, errors.New("the set is not one moment")
		}
		return fmt.Sprintf("%d files, %s, coherent after %d attempt(s)", len(m.Files), human(uint64(bytes)), m.Attempts), ms, within("the backup", took, t.Backup)
	})
	if backup == "" {
		return rep, nil
	}

	run("verify", func() (string, map[string]float64, error) {
		r, err := Verify(ctx, backup)
		if err != nil {
			return "", nil, err
		}
		ms := map[string]float64{"seconds": r.Took.Seconds(), "files": float64(r.Files), "problems": float64(len(r.Problems))}
		if !r.OK {
			return strings.Join(r.Problems, "; "), ms, errors.New("the backup does not verify")
		}
		return fmt.Sprintf("%d files verified, every checksum, integrity check and row count agrees", r.Files), ms, nil
	})

	restored := filepath.Join(work, "restored", "data")
	run("restore", func() (string, map[string]float64, error) {
		t0 := time.Now()
		r, err := Restore(ctx, backup, restored, RestoreOptions{SkipProcessCheck: true})
		took := time.Since(t0)
		if err != nil {
			return "", nil, err
		}
		ms := map[string]float64{"seconds": took.Seconds(), "stage_seconds": r.Stage.Seconds(), "swap_seconds": r.Swap.Seconds(), "bytes": float64(r.Bytes), "files": float64(r.Files)}
		mk, ok, merr := ReadRestore(restored)
		if merr != nil || !ok || mk.State != NeedsReconcile {
			return "", ms, errors.New("the restored folder does not carry a restore marker that suspends orders")
		}
		if _, err := os.Stat(filepath.Join(restored, "restored-from", "MANIFEST.json")); err != nil {
			return "", ms, errors.New("the manifest was not carried into the restored folder")
		}
		return fmt.Sprintf("restored %d files (%s) into an empty folder; orders are suspended until reconciled", r.Files, human(uint64(r.Bytes))), ms, within("the restore", took, t.Restore)
	})

	run("open-and-migrate", func() (string, map[string]float64, error) {
		known, err := store.KnownSchema()
		if err != nil {
			return "", nil, err
		}
		var pending float64
		for _, f := range man.Files {
			if f.Kind != "db" {
				continue
			}
			want := known.Tenant
			if f.Path == "base.db" {
				want = known.Base
			}
			if f.Schema < want {
				pending += float64(want - f.Schema)
			}
		}
		t0 := time.Now()
		db, err := store.Open(restored)
		if err != nil {
			return "", nil, err
		}
		took := time.Since(t0)
		db.Close()
		ms := map[string]float64{"seconds": took.Seconds(), "migrations_applied": pending}
		for _, f := range man.Files {
			if f.Kind != "db" {
				continue
			}
			sd, err := store.ReadSchema(ctx, filepath.Join(restored, filepath.FromSlash(f.Path)))
			if err != nil {
				return "", ms, err
			}
			want := known.Tenant
			if f.Path == "base.db" {
				want = known.Base
			}
			if sd.Applied != want {
				return "", ms, fmt.Errorf("%s is at schema %d after opening, expected %d", f.Path, sd.Applied, want)
			}
		}
		note := "no migration was pending: the copy was already current"
		if pending > 0 {
			note = fmt.Sprintf("%d migration(s) applied to the copy", int(pending))
		}
		return note, ms, within("opening the copy", took, t.Open)
	})

	run("interrupted-restore", func() (string, map[string]float64, error) {
		stop := errors.New("simulated stop")
		ms := map[string]float64{}
		for _, stage := range []string{"verified", "staged", "moved-aside"} {
			// a data folder that already holds something, so there is something to protect
			dd := filepath.Join(work, "interrupt-"+stage, "data")
			if _, err := Restore(ctx, backup, dd, RestoreOptions{SkipProcessCheck: true, Now: func() time.Time { return time.Now().Add(-time.Hour) }}); err != nil {
				return "", ms, err
			}
			before, err := folderCounts(ctx, dd)
			if err != nil {
				return "", ms, err
			}
			t0 := time.Now()
			_, err = Restore(ctx, backup, dd, RestoreOptions{SkipProcessCheck: true, Fault: func(s string) error {
				if s == stage {
					return stop
				}
				return nil
			}})
			if !errors.Is(err, stop) {
				return "", ms, fmt.Errorf("the stop at %q was not reached: %v", stage, err)
			}
			if stage == "moved-aside" {
				if _, err := os.Stat(dd); err == nil {
					return "", ms, errors.New("the data folder should be missing after a stop between the renames")
				}
				if err := Recover(dd); err != nil {
					return "", ms, fmt.Errorf("recover after the stop at %q: %w", stage, err)
				}
				ms["recover_seconds"] = time.Since(t0).Seconds()
			}
			after, err := folderCounts(ctx, dd)
			if err != nil {
				return "", ms, fmt.Errorf("after the stop at %q the data folder does not open: %w", stage, err)
			}
			if !sameFolder(before, after) {
				return "", ms, fmt.Errorf("after the stop at %q the data folder differs from the backup it holds", stage)
			}
			ms["stops_survived"]++
		}
		return "a stop after verifying, after staging and between the two renames each leaves a complete data folder (the last after Recover)", ms, nil
	})

	run("interrupted-backup", func() (string, map[string]float64, error) {
		root := filepath.Join(work, "interrupted-backups")
		stop := errors.New("simulated stop")
		stage := "company"
		if len(dbs) > 1 {
			stage = "database:" + dbs[1]
		}
		_, _, err := Snapshot(ctx, dataDir, root, SnapshotOptions{Fault: func(s string) error {
			if s == stage {
				return stop
			}
			return nil
		}})
		if !errors.Is(err, stop) {
			return "", nil, fmt.Errorf("the stop was not reached: %v", err)
		}
		ids, _ := List(root)
		if len(ids) != 0 {
			return "", nil, errors.New("a half-written backup is listed as a backup")
		}
		ents, _ := os.ReadDir(root)
		left := 0
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".partial") {
				left++
				if _, verr := Verify(ctx, filepath.Join(root, e.Name())); verr == nil {
					return "", nil, errors.New("a half-written backup verifies")
				}
			}
		}
		if _, _, err := Snapshot(ctx, dataDir, root, SnapshotOptions{}); err != nil {
			return "", nil, fmt.Errorf("the next backup after a stop: %w", err)
		}
		ids, _ = List(root)
		if len(ids) != 1 {
			return "", nil, errors.New("the next backup did not complete")
		}
		return "a stop mid-backup leaves a .partial folder that is neither listed nor verifiable, and the next backup completes", map[string]float64{"leftover_partial_folders": float64(left)}, nil
	})

	run("timeouts", func() (string, map[string]float64, error) {
		ms := map[string]float64{}
		expired, cancel := context.WithTimeout(ctx, time.Nanosecond)
		defer cancel()
		time.Sleep(time.Millisecond)
		root := filepath.Join(work, "timeout-backups")
		if _, _, err := Snapshot(expired, dataDir, root, SnapshotOptions{}); !errors.Is(err, context.DeadlineExceeded) {
			return "", ms, fmt.Errorf("a backup past its deadline should fail with the deadline, got %v", err)
		}
		if ids, _ := List(root); len(ids) != 0 {
			return "", ms, errors.New("a backup past its deadline left a backup behind")
		}
		ents, _ := os.ReadDir(root)
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".partial") {
				return "", ms, errors.New("a backup past its deadline left a partial folder")
			}
		}
		keep := filepath.Join(work, "timeout-data")
		if _, err := Restore(ctx, backup, keep, RestoreOptions{SkipProcessCheck: true}); err != nil {
			return "", ms, err
		}
		before, _ := folderCounts(ctx, keep)
		if _, err := Restore(expired, backup, keep, RestoreOptions{SkipProcessCheck: true}); err == nil {
			return "", ms, errors.New("a restore past its deadline should fail")
		}
		after, err := folderCounts(ctx, keep)
		if err != nil || !sameFolder(before, after) {
			return "", ms, errors.New("a restore past its deadline changed the data folder")
		}
		// a job that died holding its lease does not block the next one for ever
		clock := time.Now()
		ls := Leases{Dir: filepath.Join(work, "leases"), Now: func() time.Time { return clock }}
		h1, err := ls.Acquire("drill", "crashed", time.Minute)
		if err != nil {
			return "", ms, err
		}
		if _, err := ls.Acquire("drill", "second", time.Minute); err == nil {
			return "", ms, errors.New("a second instance took a live lease")
		}
		clock = clock.Add(2 * time.Minute)
		h2, err := ls.Acquire("drill", "second", time.Minute)
		if err != nil {
			return "", ms, fmt.Errorf("the lease of a dead job was not taken over: %w", err)
		}
		if h1.Valid() || !h2.Valid() || h2.Generation() <= h1.Generation() {
			return "", ms, errors.New("the old owner still believes it holds the lease")
		}
		ms["lease_generation_after_takeover"] = float64(h2.Generation())
		return "a backup and a restore past their deadline fail cleanly and change nothing; an expired lease is taken over with a higher generation and the old owner is told it lost it", ms, nil
	})

	run("mirror", func() (string, map[string]float64, error) {
		t0 := time.Now()
		res, err := Mirror(ctx, backup, filepath.Join(work, "mirror"))
		if err != nil {
			return "", nil, err
		}
		note := "the mirror is on another filesystem"
		if res.SameDevice {
			note = "the mirror is on the same filesystem as the backup (as it must be in a drill): it guards against deletion, not against the disk failing"
		}
		return "copied and verified; " + note, map[string]float64{"seconds": time.Since(t0).Seconds()}, nil
	})
	return rep, nil
}

// folderCounts opens every database of a data folder read-only and returns the
// rows of every table, by database and table.
func folderCounts(ctx context.Context, dir string) (map[string]int64, error) {
	dbs, err := Databases(dir)
	if err != nil {
		return nil, err
	}
	if len(dbs) == 0 {
		return nil, errors.New("no database")
	}
	out := map[string]int64{}
	for _, d := range dbs {
		p := filepath.Join(dir, filepath.FromSlash(d))
		if err := integrity(ctx, p); err != nil {
			return nil, fmt.Errorf("%s: %w", d, err)
		}
		c, err := tableCounts(ctx, p)
		if err != nil {
			return nil, err
		}
		for t, n := range c {
			out[d+"."+t] = n
		}
	}
	return out, nil
}

func sameFolder(a, b map[string]int64) bool { return sameCounts(a, b) }
