package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

// Health levels.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
)

// DBHealth is one database. Names are "base" and "tenant-1", "tenant-2" ...,
// never the tenant's own name or a path.
type DBHealth struct {
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	WAL     int64  `json:"wal_bytes"`
	Applied int    `json:"schema_applied"`
	Known   int    `json:"schema_known"`
	State   string `json:"state"` // current, behind, ahead or missing
}

// LeaseHealth is one job lease. The holder is not shown.
type LeaseHealth struct {
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	ExpiresIn int64  `json:"expires_in_seconds"`
}

// Health is a redacted picture of the installation: sizes, versions, ages and
// states. It holds no path, tenant name, address, key or token, so it can be
// pasted into a ticket or scraped by a monitor.
type Health struct {
	Status   string        `json:"status"`
	Checked  time.Time     `json:"checked"`
	DBs      []DBHealth    `json:"databases"`
	Disk     DiskHealth    `json:"disk"`
	Backup   BackupHealth  `json:"backup"`
	Leases   []LeaseHealth `json:"leases"`
	Restore  string        `json:"restore"` // none, needs_reconcile or reconciled
	Problems []string      `json:"problems"`
}

// DiskHealth is the space.
type DiskHealth struct {
	Free        uint64 `json:"free_bytes"`
	Total       uint64 `json:"total_bytes"`
	Data        uint64 `json:"data_bytes"`
	MinFree     uint64 `json:"min_free_bytes"`
	MaxData     uint64 `json:"max_data_bytes"`
	Interrupted bool   `json:"interrupted_restore"`
}

// BackupHealth is the newest backup.
type BackupHealth struct {
	Count      int    `json:"count"`
	Newest     string `json:"newest,omitempty"`
	AgeSeconds int64  `json:"newest_age_seconds"`
	Coherent   bool   `json:"newest_coherent"`
}

// HealthOptions say where to look and what is too old.
type HealthOptions struct {
	DataDir string
	// PostgresURL, when set, says the data is in that Postgres database and not
	// in SQLite files: the databases listed are its schemas, read without
	// writing. The URL never appears in the result.
	PostgresURL  string
	BackupRoot   string
	Quota        Quota
	MaxBackupAge time.Duration // older than this is a warning; 0 means 48 hours
	Now          func() time.Time
}

// Check reads the installation without opening any database for writing and
// without migrating it.
func Check(ctx context.Context, o HealthOptions) (Health, error) {
	now := time.Now().UTC()
	if o.Now != nil {
		now = o.Now().UTC()
	}
	maxAge := o.MaxBackupAge
	if maxAge == 0 {
		maxAge = 48 * time.Hour
	}
	h := Health{Checked: now, DBs: []DBHealth{}, Leases: []LeaseHealth{}, Problems: []string{}, Restore: "none"}
	level := OK
	bump := func(l, msg string) {
		h.Problems = append(h.Problems, l+": "+msg)
		if l == Fail || (l == Warn && level == OK) {
			level = l
		}
	}
	known, err := store.KnownSchema()
	if err != nil {
		return h, err
	}
	if _, err := os.Stat(o.DataDir); err != nil {
		bump(Fail, "the data folder is missing")
		if _, serr := os.ReadDir(filepath.Dir(o.DataDir)); serr == nil {
			ents, _ := os.ReadDir(filepath.Dir(o.DataDir))
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), filepath.Base(o.DataDir)+".restore-") || strings.HasPrefix(e.Name(), filepath.Base(o.DataDir)+".before-restore-") {
					h.Disk.Interrupted = true
				}
			}
		}
		if h.Disk.Interrupted {
			bump(Fail, "a restore was interrupted between its two renames: run ops recover")
		}
		h.Status = level
		return h, nil
	}
	var dbs []string
	if o.PostgresURL != "" {
		pgHealth(ctx, o.PostgresURL, known, &h, bump)
	} else if dbs, err = Databases(o.DataDir); err != nil {
		return h, err
	}
	n := 0
	for _, d := range dbs {
		name := "base"
		kn := known.Base
		if d != "base.db" {
			n++
			name = fmt.Sprintf("tenant-%d", n)
			kn = known.Tenant
		}
		p := filepath.Join(o.DataDir, filepath.FromSlash(d))
		dh := DBHealth{Name: name, Known: kn, State: "current"}
		if st, err := os.Stat(p); err == nil {
			dh.Bytes = st.Size()
		}
		if st, err := os.Stat(p + "-wal"); err == nil {
			dh.WAL = st.Size()
		}
		sd, err := store.ReadSchema(ctx, p)
		if err != nil {
			bump(Fail, name+" cannot be read")
			dh.State = "unreadable"
		} else {
			dh.Applied = sd.Applied
			switch {
			case sd.Applied > kn:
				dh.State = "ahead"
				bump(Fail, fmt.Sprintf("%s is at schema %d and this program knows %d: it was written by a newer program", name, sd.Applied, kn))
			case sd.Applied < kn:
				dh.State = "behind"
				bump(Warn, fmt.Sprintf("%s is at schema %d and will migrate to %d when next opened: take a backup first", name, sd.Applied, kn))
			}
		}
		h.DBs = append(h.DBs, dh)
	}
	if o.PostgresURL == "" && (len(dbs) == 0 || dbs[0] != "base.db") {
		bump(Warn, "there is no base database yet")
	}

	d, err := DiskOf(o.DataDir)
	if err != nil {
		bump(Warn, "disk space is unknown")
	} else {
		h.Disk.Free, h.Disk.Total = d.Free, d.Total
	}
	used, _ := DirSize(o.DataDir)
	h.Disk.Data, h.Disk.MinFree, h.Disk.MaxData = used, o.Quota.MinFree, o.Quota.MaxData
	if q := o.Quota; q.MinFree > 0 && h.Disk.Free < q.MinFree {
		bump(Fail, fmt.Sprintf("only %s is free and %s must stay free: fetching and backups are refused", human(h.Disk.Free), human(q.MinFree)))
	} else if q.MinFree > 0 && h.Disk.Free < 2*q.MinFree {
		bump(Warn, fmt.Sprintf("free space is %s, under twice the %s that must stay free", human(h.Disk.Free), human(q.MinFree)))
	}
	if q := o.Quota; q.MaxData > 0 && used > q.MaxData {
		bump(Fail, fmt.Sprintf("the data folder holds %s and may hold %s", human(used), human(q.MaxData)))
	} else if q.MaxData > 0 && used*10 > q.MaxData*9 {
		bump(Warn, "the data folder is over 90% of its limit")
	}

	if o.BackupRoot != "" {
		ids, _ := List(o.BackupRoot)
		h.Backup.Count = len(ids)
		if len(ids) == 0 {
			bump(Warn, "there is no backup")
		} else {
			h.Backup.Newest = ids[0]
			if m, err := ReadManifest(filepath.Join(o.BackupRoot, ids[0])); err == nil {
				h.Backup.AgeSeconds = int64(now.Sub(m.Taken).Seconds())
				h.Backup.Coherent = m.Coherent
				if now.Sub(m.Taken) > maxAge {
					bump(Warn, fmt.Sprintf("the newest backup is %s old", now.Sub(m.Taken).Round(time.Minute)))
				}
				if !m.Coherent {
					bump(Warn, "the newest backup was not taken in a quiet moment")
				}
			} else {
				bump(Warn, "the newest backup has no readable manifest")
			}
		}
	}

	leases, _ := (Leases{Dir: filepath.Join(o.DataDir, "leases"), Now: o.Now}).List()
	for _, l := range leases {
		h.Leases = append(h.Leases, LeaseHealth{Name: l.Name, Active: l.Active, ExpiresIn: int64(l.Expires.Sub(now).Seconds())})
	}
	if m, ok, err := ReadRestore(o.DataDir); err != nil {
		bump(Fail, "the restore marker cannot be read")
	} else if ok {
		h.Restore = m.State
		if m.State == NeedsReconcile {
			bump(Warn, "restored from backup "+m.BackupID+" and not reconciled with the broker: new orders are refused (ops reconciled)")
		}
	}
	sort.Strings(h.Problems)
	h.Status = level
	return h, nil
}

// pgHealth lists the schemas of a Postgres database as databases. It reads the
// migration tables and nothing else.
func pgHealth(ctx context.Context, url string, known store.SchemaVersions, h *Health, bump func(l, msg string)) {
	st, err := store.InspectPostgres(ctx, url, false)
	if err != nil {
		bump(Fail, "Postgres cannot be reached or read")
		return
	}
	names := make([]string, 0, len(st.Schemas))
	for s := range st.Schemas {
		names = append(names, s)
	}
	sort.Strings(names) // "base" sorts before "tenant_..."
	n := 0
	for _, s := range names {
		name := "base"
		if s != "base" {
			n++
			name = fmt.Sprintf("tenant-%d", n)
		}
		kn := known.KnownFor(s)
		dh := DBHealth{Name: name, Known: kn, Applied: st.Schemas[s], State: "current"}
		switch {
		case dh.Applied > kn:
			dh.State = "ahead"
			bump(Fail, fmt.Sprintf("%s is at schema %d and this program knows %d: it was written by a newer program", name, dh.Applied, kn))
		case dh.Applied < kn:
			dh.State = "behind"
			bump(Warn, fmt.Sprintf("%s is at schema %d and will migrate to %d when next opened: take a backup first", name, dh.Applied, kn))
		}
		h.DBs = append(h.DBs, dh)
	}
	if _, ok := st.Schemas["base"]; !ok {
		bump(Warn, "there is no base schema yet")
	}
}

// Metrics renders the health in the Prometheus text format. Labels come from a
// fixed set; there is no path, tenant name or free text.
func Metrics(h Health) string {
	var b strings.Builder
	g := func(name, help string, v any, labels ...string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		if len(labels) == 0 {
			fmt.Fprintf(&b, "%s %v\n", name, v)
		}
	}
	line := func(name string, v any, lbl string) {
		if lbl == "" {
			fmt.Fprintf(&b, "%s %v\n", name, v)
			return
		}
		fmt.Fprintf(&b, "%s{%s} %v\n", name, lbl, v)
	}
	status := map[string]int{OK: 0, Warn: 1, Fail: 2}[h.Status]
	g("stoxicity_health_status", "0 ok, 1 warning, 2 failing", status)
	g("stoxicity_disk_free_bytes", "free space on the data filesystem", h.Disk.Free)
	g("stoxicity_data_bytes", "size of the data folder", h.Disk.Data)
	g("stoxicity_backup_count", "backups found", h.Backup.Count)
	g("stoxicity_backup_newest_age_seconds", "age of the newest backup", h.Backup.AgeSeconds)
	fmt.Fprintf(&b, "# HELP stoxicity_db_bytes size of a database file\n# TYPE stoxicity_db_bytes gauge\n")
	for _, d := range h.DBs {
		line("stoxicity_db_bytes", d.Bytes, `db="`+d.Name+`"`)
	}
	fmt.Fprintf(&b, "# HELP stoxicity_db_wal_bytes size of a write-ahead log\n# TYPE stoxicity_db_wal_bytes gauge\n")
	for _, d := range h.DBs {
		line("stoxicity_db_wal_bytes", d.WAL, `db="`+d.Name+`"`)
	}
	fmt.Fprintf(&b, "# HELP stoxicity_db_schema_current 1 when the schema is the one this program knows\n# TYPE stoxicity_db_schema_current gauge\n")
	for _, d := range h.DBs {
		v := 0
		if d.State == "current" {
			v = 1
		}
		line("stoxicity_db_schema_current", v, `db="`+d.Name+`"`)
	}
	fmt.Fprintf(&b, "# HELP stoxicity_lease_active 1 while a job lease is held\n# TYPE stoxicity_lease_active gauge\n")
	for _, l := range h.Leases {
		v := 0
		if l.Active {
			v = 1
		}
		line("stoxicity_lease_active", v, `job="`+l.Name+`"`)
	}
	pending := 0
	if h.Restore == NeedsReconcile {
		pending = 1
	}
	g("stoxicity_restore_pending", "1 while a restore has not been reconciled with the broker", pending)
	return b.String()
}
