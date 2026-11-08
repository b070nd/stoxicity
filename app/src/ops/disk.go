// Package ops is how the program is run and recovered: leases that keep one
// instance of a job running, limits on disk and data size, a coherent backup of
// every database with a manifest, a restore that never destroys what it
// replaces, redacted health and metrics, service files, and drills that measure
// all of it on copies.
//
// Nothing here opens the databases through the application's store, so nothing
// here migrates anything. Databases are read through read-only connections and
// copied with SQLite's own snapshot (VACUUM INTO); a live database is never
// written.
package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Disk is the space on the filesystem that holds a folder.
type Disk struct {
	Free, Total uint64 // bytes
}

// DiskOf reports the space available to an unprivileged user.
func DiskOf(dir string) (Disk, error) {
	// A folder that does not exist yet (the first fetch creates it) lives on the
	// filesystem of its nearest existing parent.
	for {
		if _, err := os.Stat(dir); err == nil || !errors.Is(err, fs.ErrNotExist) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return Disk{}, fmt.Errorf("ops: disk of %s: %w", filepath.Base(dir), err)
	}
	bs := uint64(st.Bsize)
	return Disk{Free: uint64(st.Bavail) * bs, Total: uint64(st.Blocks) * bs}, nil
}

// DirSize is the total size in bytes of the regular files under dir.
func DirSize(dir string) (uint64, error) {
	var n uint64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			n += uint64(info.Size())
		}
		return nil
	})
	return n, err
}

// Quota holds the limits. A zero field means no limit.
type Quota struct {
	// MinFree is the free space the filesystem must keep, in bytes.
	MinFree uint64
	// MaxData is the most the data folder may hold, in bytes.
	MaxData uint64
}

// LowDisk is returned when a limit is reached. Callers refuse the heavy work
// (fetching, writing a backup) and let the small, important work go on:
// cancelling, reconciling and delivering an alert are never refused for space.
type LowDisk struct {
	Reason string
}

func (e *LowDisk) Error() string { return "ops: " + e.Reason }

// Check says whether the work may start. need is the extra bytes the work is
// about to write, 0 when it is not known.
func (q Quota) Check(dataDir string, need uint64) error {
	d, err := DiskOf(dataDir)
	if err != nil {
		return err
	}
	if q.MinFree > 0 && d.Free < q.MinFree+need {
		return &LowDisk{fmt.Sprintf("only %s is free and %s must stay free (this work needs about %s more)", human(d.Free), human(q.MinFree), human(need))}
	}
	if q.MaxData > 0 {
		used, err := DirSize(dataDir)
		if err != nil {
			return err
		}
		if used+need > q.MaxData {
			return &LowDisk{fmt.Sprintf("the data folder holds %s and may hold %s (this work needs about %s more)", human(used), human(q.MaxData), human(need))}
		}
	}
	return nil
}

func human(n uint64) string {
	const u = 1024
	switch {
	case n >= u*u*u:
		return fmt.Sprintf("%.1f GiB", float64(n)/(u*u*u))
	case n >= u*u:
		return fmt.Sprintf("%.1f MiB", float64(n)/(u*u))
	case n >= u:
		return fmt.Sprintf("%.1f KiB", float64(n)/u)
	}
	return fmt.Sprintf("%d B", n)
}

// SameDevice reports whether two paths are on one filesystem. A backup mirror
// on the same one protects against deletion, not against the disk failing.
func SameDevice(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	sa, ok1 := ia.Sys().(*syscall.Stat_t)
	sb, ok2 := ib.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false, errors.New("ops: cannot tell which device a path is on")
	}
	return sa.Dev == sb.Dev, nil
}
