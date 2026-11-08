package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

// dataFolder builds a real data folder: the base database, a tenant database
// with records, and a company file.
func dataFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, _, err := db.Records("acme").Append(context.Background(), store.RecordInput{Kind: "note", Key: fmt.Sprint("k", i), Body: []byte(fmt.Sprintf(`{"n":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "company"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "company", "list.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func appendRecord(t *testing.T, dir, key string) {
	t.Helper()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, _, err := db.Records("acme").Append(context.Background(), store.RecordInput{Kind: "note", Key: key, Body: []byte(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
}

func fixedNow(s string) func() time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return func() time.Time { return t }
}

func TestLeaseHeldRenewedExpiredAndTakenOver(t *testing.T) {
	clock := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	ls := Leases{Dir: t.TempDir(), Now: func() time.Time { return clock }}
	a, err := ls.Acquire("alerts", "job-a", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var held *ErrHeld
	if _, err := ls.Acquire("alerts", "job-b", time.Minute); !errors.As(err, &held) || held.Holder != "job-a" || !held.Until.Equal(clock.Add(10*time.Minute)) {
		t.Fatalf("a live lease is not taken: %v", err)
	}
	if _, err := ls.Acquire("other", "job-b", time.Minute); err != nil {
		t.Errorf("leases of other names are independent: %v", err)
	}
	clock = clock.Add(5 * time.Minute)
	if err := a.Renew(10 * time.Minute); err != nil || !a.Valid() {
		t.Fatalf("renew: %v", err)
	}
	clock = clock.Add(11 * time.Minute) // the owner died and the lease ran out
	if a.Valid() {
		t.Fatal("an expired lease is not valid")
	}
	b, err := ls.Acquire("alerts", "job-b", time.Minute)
	if err != nil {
		t.Fatalf("an expired lease is taken over: %v", err)
	}
	if b.Generation() <= a.Generation() {
		t.Errorf("fencing: generation %d after %d", b.Generation(), a.Generation())
	}
	if err := a.Renew(time.Minute); !errors.Is(err, ErrLost) {
		t.Errorf("the old owner is told it lost the lease: %v", err)
	}
	a.Release() // must not release b's
	if !b.Valid() {
		t.Error("releasing a lost lease released the new owner's")
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if c, err := ls.Acquire("alerts", "job-c", time.Minute); err != nil {
		t.Errorf("a released lease is free: %v", err)
	} else if c.Generation() <= b.Generation() {
		t.Error("generations only grow")
	}
	infos, _ := ls.List()
	if len(infos) != 2 || infos[0].Name != "alerts" || !infos[0].Active {
		t.Errorf("%+v", infos)
	}
	if _, err := ls.Acquire("Bad Name", "x", time.Minute); err == nil {
		t.Error("a lease name is checked")
	}
}

func TestLeaseSurvivesAWinnerThatDiedBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	ls := Leases{Dir: dir}
	// a generation marker with no lease behind it, long ago
	m := filepath.Join(dir, "job.gen1")
	os.WriteFile(m, nil, 0o600)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(m, old, old)
	h, err := ls.Acquire("job", "x", time.Minute)
	if err != nil {
		t.Fatalf("a dead winner must not block for ever: %v", err)
	}
	if h.Generation() < 2 {
		t.Errorf("generation %d", h.Generation())
	}
}

func TestQuota(t *testing.T) {
	dir := dataFolder(t)
	if err := (Quota{}).Check(dir, 0); err != nil {
		t.Fatal(err)
	}
	var low *LowDisk
	if err := (Quota{MinFree: 1 << 62}).Check(dir, 0); !errors.As(err, &low) || !strings.Contains(low.Reason, "must stay free") {
		t.Errorf("%v", err)
	}
	if err := (Quota{MaxData: 1}).Check(dir, 0); !errors.As(err, &low) || !strings.Contains(low.Reason, "may hold") {
		t.Errorf("%v", err)
	}
	if err := (Quota{MaxData: 1 << 40}).Check(dir, 1<<41); err == nil {
		t.Error("the work that is about to be written counts")
	}
}

func TestSnapshotVerifyAndTheShellToolAgree(t *testing.T) {
	dir := dataFolder(t)
	root := t.TempDir()
	dest, m, err := Snapshot(context.Background(), dir, root, SnapshotOptions{Now: fixedNow("2026-11-08T02:30:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest) != "20261108T023000Z" || !m.Coherent || m.Attempts != 1 || m.Format != BackupFormat {
		t.Fatalf("%+v", m)
	}
	var sawBase, sawTenant, sawCompany bool
	for _, f := range m.Files {
		switch {
		case f.Path == "base.db":
			sawBase = f.Schema > 0 && len(f.Tables) > 0
		case f.Path == "tenants/acme.db":
			sawTenant = f.Tables["records"] == 5 && f.Schema > 0
		case f.Path == "company/list.json":
			sawCompany = f.Kind == "file"
		}
		if f.SHA256 == "" || f.Bytes == 0 {
			t.Errorf("%+v", f)
		}
	}
	if !sawBase || !sawTenant || !sawCompany {
		t.Fatalf("every database and the company files are in the manifest: %+v", m.Files)
	}
	rep, err := Verify(context.Background(), dest)
	if err != nil || !rep.OK || rep.Files != len(m.Files) {
		t.Fatalf("%+v %v", rep, err)
	}
	// the live database was only read
	if _, err := os.Stat(filepath.Join(dir, "tenants", "acme.db")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Snapshot(context.Background(), dir, root, SnapshotOptions{Now: fixedNow("2026-11-08T02:30:00Z")}); err == nil {
		t.Error("a backup is never overwritten")
	}
	// the shell tool reads what this one wrote
	if sh, err := exec.LookPath("sh"); err == nil {
		if _, e1 := exec.LookPath("sqlite3"); e1 == nil {
			if _, e2 := exec.LookPath("shasum"); e2 == nil {
				out, err := exec.Command(sh, "../../../backup.sh", "verify", dest).CombinedOutput()
				if err != nil || !strings.Contains(string(out), "intact") {
					t.Errorf("backup.sh verify: %v\n%s", err, out)
				}
			}
		}
	}
}

func TestVerifyFindsEveryKindOfDamage(t *testing.T) {
	dir := dataFolder(t)
	root := t.TempDir()
	dest, _, err := Snapshot(context.Background(), dir, root, SnapshotOptions{Now: fixedNow("2026-11-08T02:30:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	// an extra file
	os.WriteFile(filepath.Join(dest, "stray.txt"), []byte("x"), 0o644)
	// a changed file
	os.WriteFile(filepath.Join(dest, "company", "list.json"), []byte(`{"a":2}`), 0o644)
	// a missing database
	os.Remove(filepath.Join(dest, "tenants", "acme.db"))
	rep, err := Verify(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rep.Problems, "\n")
	if rep.OK || !strings.Contains(joined, "stray.txt: not in the manifest") || !strings.Contains(joined, "company/list.json: the file differs") || !strings.Contains(joined, "tenants/acme.db") {
		t.Errorf("%+v", rep)
	}
	if _, err := Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("a folder that is no backup")
	}
}

func TestVerifyRefusesABackupFromANewerProgram(t *testing.T) {
	dir := dataFolder(t)
	raw, err := sql.Open("sqlite", filepath.Join(dir, "tenants", "acme.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_migrations (version, name, applied_ms) VALUES (9999, 'from the future', 0)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	dest, _, err := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := Verify(context.Background(), dest)
	if rep.OK || !strings.Contains(strings.Join(rep.Problems, ";"), "written by a newer program") {
		t.Errorf("%+v", rep)
	}
	h, err := Check(context.Background(), HealthOptions{DataDir: dir})
	if err != nil || h.Status != Fail {
		t.Fatalf("%+v %v", h, err)
	}
}

func TestSnapshotIsOneMomentOrSaysItIsNot(t *testing.T) {
	dir := dataFolder(t)
	// a write lands between two databases of the set, once: the second try is coherent
	wrote := false
	dest, m, err := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{Fault: func(stage string) error {
		if strings.HasPrefix(stage, "database:tenants") && !wrote {
			wrote = true
			appendRecord(t, dir, "late")
		}
		return nil
	}})
	if err != nil || !m.Coherent || m.Attempts != 2 {
		t.Fatalf("%+v %v", m, err)
	}
	rep, _ := Verify(context.Background(), dest)
	if !rep.OK {
		t.Fatal(rep.Problems)
	}
	// writes that never stop: refused, or kept and marked
	n := 0
	always := func(stage string) error {
		if strings.HasPrefix(stage, "database:tenants") {
			n++
			appendRecord(t, dir, fmt.Sprint("busy", n))
		}
		return nil
	}
	if _, _, err := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{Fault: always}); err == nil || !strings.Contains(err.Error(), "kept changing") {
		t.Fatalf("a set that is not one moment is refused: %v", err)
	}
	root := t.TempDir()
	_, m, err = Snapshot(context.Background(), dir, root, SnapshotOptions{Fault: always, AllowIncoherent: true})
	if err != nil || m.Coherent {
		t.Fatalf("kept but marked: %+v %v", m, err)
	}
	if ids, _ := List(root); len(ids) != 1 {
		t.Error("kept")
	}
}

func fakeBackups(t *testing.T, ids ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, id := range ids {
		os.MkdirAll(filepath.Join(root, id), 0o755)
		os.WriteFile(filepath.Join(root, id, "MANIFEST.json"), []byte(`{}`), 0o644)
	}
	return root
}

func TestRotateKeepsRecentAndOnePerWeekAndNeverTheNewestOrProtected(t *testing.T) {
	root := fakeBackups(t,
		"20261108T023000Z", "20261107T023000Z", "20261106T023000Z", "20261105T023000Z", // this week (Sun 8 Nov ends ISO week 45)
		"20261101T023000Z", "20261031T023000Z", // week 44
		"20261025T023000Z", "20261018T023000Z", "20261001T023000Z")
	os.MkdirAll(filepath.Join(root, "20261108T023000Z.partial"), 0o755)
	os.MkdirAll(filepath.Join(root, "notes"), 0o755)
	removed, err := Rotate(root, 2, 3, "20261001T023000Z")
	if err != nil {
		t.Fatal(err)
	}
	left, _ := List(root)
	want := []string{"20261108T023000Z", "20261107T023000Z", "20261101T023000Z", "20261025T023000Z", "20261001T023000Z"}
	if strings.Join(left, " ") != strings.Join(want, " ") {
		t.Errorf("left %v (removed %v), want %v", left, removed, want)
	}
	for _, p := range []string{"20261108T023000Z.partial", "notes"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s is not a backup and is not touched", p)
		}
	}
	if _, err := Rotate(root, 0, 0); err == nil {
		t.Error("keeping nothing is refused")
	}
}

func TestMirrorVerifiesAndSaysHowIndependentItIs(t *testing.T) {
	dir := dataFolder(t)
	dest, _, err := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mroot := t.TempDir()
	res, err := Mirror(context.Background(), dest, mroot)
	if err != nil || !res.Report.OK || res.Path != filepath.Join(mroot, filepath.Base(dest)) {
		t.Fatalf("%+v %v", res, err)
	}
	if !res.SameDevice {
		t.Log("temp folders on different devices")
	}
	if _, err := Mirror(context.Background(), dest, mroot); err == nil {
		t.Error("a mirror is never overwritten")
	}
	if _, err := Mirror(context.Background(), filepath.Join(t.TempDir(), "stuff"), mroot); err == nil {
		t.Error("only backups are mirrored")
	}
}

func counts(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	c, err := folderCounts(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRestoreReplacesWithoutDestroyingAndSuspendsOrders(t *testing.T) {
	dir := dataFolder(t)
	want := counts(t, dir)
	backup, _, err := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{Now: fixedNow("2026-11-08T02:30:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	appendRecord(t, dir, "after-the-backup")
	if sameFolder(want, counts(t, dir)) {
		t.Fatal("the live data should have moved on")
	}
	res, err := Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true, Now: fixedNow("2026-11-08T09:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if !sameFolder(want, counts(t, dir)) {
		t.Error("the data is the backup's")
	}
	aside := dir + ".before-restore-20261108T090000Z"
	if res.Previous != filepath.Base(aside) {
		t.Errorf("%+v", res)
	}
	if c := counts(t, aside); c["tenants/acme.db.records"] != want["tenants/acme.db.records"]+1 {
		t.Errorf("the folder it replaced is kept, with what was written after the backup: %v", c)
	}
	for _, f := range []string{"restored-from/MANIFEST.json", "restored-from/MANIFEST", "company/list.json", RestoreFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".staged")); err == nil {
		t.Error("the staging marker is gone")
	}
	mk, ok, err := ReadRestore(dir)
	if err != nil || !ok || mk.State != NeedsReconcile || mk.BackupID != "20261108T023000Z" || mk.Previous != filepath.Base(aside) {
		t.Fatalf("%+v %v %v", mk, ok, err)
	}
	if s := Suspension(dir); !strings.Contains(s, "20261108T023000Z") || !strings.Contains(s, "ops reconciled") {
		t.Errorf("%q", s)
	}
	mk.State = Reconciled
	WriteRestore(dir, mk)
	if s := Suspension(dir); s != "" {
		t.Errorf("reconciled: %q", s)
	}
	os.WriteFile(filepath.Join(dir, RestoreFile), []byte("{broken"), 0o644)
	if s := Suspension(dir); !strings.Contains(s, "cannot be read") {
		t.Errorf("not knowing is not a reason to send orders: %q", s)
	}
	if Suspension("") != "" || Suspension(t.TempDir()) != "" {
		t.Error("a folder never restored is not suspended")
	}
}

func TestRestoreChangesNothingWhenItCannotProceed(t *testing.T) {
	dir := dataFolder(t)
	backup, _, err := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := counts(t, dir)
	// a lease that is live
	ls := Leases{Dir: filepath.Join(dir, "leases")}
	h, _ := ls.Acquire("alerts-deliver", "job", time.Hour)
	if _, err := Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true}); err == nil || !strings.Contains(err.Error(), "holds a lease") {
		t.Fatalf("%v", err)
	}
	h.Release()
	// a database held for writing
	raw, _ := sql.Open("sqlite", filepath.Join(dir, "tenants", "acme.db"))
	conn, _ := raw.Conn(context.Background())
	conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`)
	if _, err := Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true}); err == nil || !strings.Contains(err.Error(), "being written") {
		t.Fatalf("%v", err)
	}
	conn.ExecContext(context.Background(), `ROLLBACK`)
	conn.Close()
	raw.Close()
	// a damaged backup
	os.WriteFile(filepath.Join(backup, "company", "list.json"), []byte("changed"), 0o644)
	if _, err := Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true}); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("%v", err)
	}
	if !sameFolder(before, counts(t, dir)) {
		t.Error("nothing changed")
	}
	if _, ok, _ := ReadRestore(dir); ok {
		t.Error("no marker was written")
	}
	ents, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range ents {
		if strings.Contains(e.Name(), "restore") {
			t.Errorf("a leftover: %s", e.Name())
		}
	}
}

func TestRestoreStoppedAtAnyPointLeavesAUsableFolder(t *testing.T) {
	stop := errors.New("stop")
	for _, stage := range []string{"verified", "staged", "moved-aside"} {
		dir := dataFolder(t)
		backup, _, err := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		want := counts(t, dir)
		_, err = Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true, Fault: func(s string) error {
			if s == stage {
				return stop
			}
			return nil
		}})
		if !errors.Is(err, stop) {
			t.Fatalf("%s: %v", stage, err)
		}
		if err := Recover(dir); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if !sameFolder(want, counts(t, dir)) {
			t.Errorf("%s: the data folder differs", stage)
		}
		// and a later restore goes through
		if _, err := Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true, Now: func() time.Time { return time.Now().Add(time.Hour) }}); err != nil {
			t.Errorf("%s: %v", stage, err)
		}
	}
	// a stop between the renames, with the next restore (not Recover) as the first thing run
	dir := dataFolder(t)
	backup, _, _ := Snapshot(context.Background(), dir, t.TempDir(), SnapshotOptions{})
	Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true, Fault: func(s string) error {
		if s == "moved-aside" {
			return stop
		}
		return nil
	}})
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the folder should be missing")
	}
	h, _ := Check(context.Background(), HealthOptions{DataDir: dir})
	if h.Status != Fail || !strings.Contains(strings.Join(h.Problems, ";"), "interrupted") {
		t.Errorf("health tells: %+v", h)
	}
	if _, err := Restore(context.Background(), backup, dir, RestoreOptions{SkipProcessCheck: true, Now: func() time.Time { return time.Now().Add(time.Hour) }}); err != nil {
		t.Fatal(err)
	}
}

func TestHealthAndMetricsAreRedacted(t *testing.T) {
	dir := dataFolder(t)
	root := t.TempDir()
	if _, _, err := Snapshot(context.Background(), dir, root, SnapshotOptions{Now: fixedNow("2026-11-08T02:30:00Z")}); err != nil {
		t.Fatal(err)
	}
	ls := Leases{Dir: filepath.Join(dir, "leases"), Now: fixedNow("2026-11-08T12:00:00Z")}
	if _, err := ls.Acquire("alerts-deliver", "pid 4242 on secret-host", time.Hour); err != nil {
		t.Fatal(err)
	}
	h, err := Check(context.Background(), HealthOptions{DataDir: dir, BackupRoot: root, Now: fixedNow("2026-11-08T12:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != OK || len(h.DBs) != 2 || h.DBs[0].Name != "base" || h.DBs[1].Name != "tenant-1" || h.DBs[0].State != "current" {
		t.Fatalf("%+v", h)
	}
	if h.Backup.Count != 1 || h.Backup.AgeSeconds != int64((9*time.Hour+30*time.Minute).Seconds()) || !h.Backup.Coherent {
		t.Errorf("%+v", h.Backup)
	}
	raw, _ := json.Marshal(h)
	text := string(raw) + Metrics(h)
	for _, secret := range []string{dir, "acme", "secret-host", "4242", filepath.Base(filepath.Dir(dir))} {
		if strings.Contains(text, secret) {
			t.Errorf("health leaks %q", secret)
		}
	}
	m := Metrics(h)
	for _, want := range []string{"stoxicity_health_status 0", `stoxicity_db_bytes{db="tenant-1"}`, `stoxicity_lease_active{job="alerts-deliver"} 1`, "stoxicity_restore_pending 0", "stoxicity_backup_newest_age_seconds 34200"} {
		if !strings.Contains(m, want) {
			t.Errorf("metrics lack %q:\n%s", want, m)
		}
	}
	// old backup, none, low disk
	h, _ = Check(context.Background(), HealthOptions{DataDir: dir, BackupRoot: root, Now: fixedNow("2026-11-12T12:00:00Z")})
	if h.Status != Warn || !strings.Contains(strings.Join(h.Problems, ";"), "old") {
		t.Errorf("%+v", h)
	}
	h, _ = Check(context.Background(), HealthOptions{DataDir: dir, BackupRoot: t.TempDir()})
	if h.Status != Warn || !strings.Contains(strings.Join(h.Problems, ";"), "no backup") {
		t.Errorf("%+v", h)
	}
	h, _ = Check(context.Background(), HealthOptions{DataDir: dir, Quota: Quota{MinFree: 1 << 62}})
	if h.Status != Fail {
		t.Errorf("%+v", h)
	}
}

func TestServiceFiles(t *testing.T) {
	spec := ServiceSpec{Kind: "launchd", Binary: "/opt/stox & co/stoxicity", ConfigDir: "/Users/me/stox", LogDir: "/Users/me/logs", Mirror: "/Volumes/usb"}
	files, err := Service(spec)
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	for _, f := range files {
		var v struct{ XMLName xml.Name }
		if err := xml.Unmarshal([]byte(f.Content), &v); err != nil {
			t.Errorf("%s is not well-formed: %v", f.Name, err)
		}
		if !strings.HasPrefix(f.Name, "dev.stoxicity.") || !strings.Contains(f.Content, "/opt/stox &amp; co/stoxicity") {
			t.Errorf("%s:\n%s", f.Name, f.Content)
		}
	}
	if !strings.Contains(files[0].Content, "<string>-deliver</string>") || !strings.Contains(files[0].Content, "StartInterval") || !strings.Contains(files[1].Content, "/Volumes/usb") || !strings.Contains(files[1].Content, "StartCalendarInterval") {
		t.Error("the jobs")
	}
	spec.Kind, spec.EnvFile = "systemd", "/home/me/stox/.env"
	files, err = Service(spec)
	if err != nil || len(files) != 4 {
		t.Fatal(files, err)
	}
	svc := files[0].Content
	if !strings.Contains(svc, `ExecStart="/opt/stox & co/stoxicity" "-config-dir" "/Users/me/stox" "alerts" "scan" "-deliver"`) || !strings.Contains(svc, "EnvironmentFile=-/home/me/stox/.env") || !strings.Contains(svc, "NoNewPrivileges=yes") {
		t.Errorf("%s", svc)
	}
	if !strings.Contains(files[1].Content, "OnCalendar=*:0/15") || !strings.Contains(files[1].Content, "Persistent=true") {
		t.Error(files[1].Content)
	}
	if sd(`a"b%c$d\`) != `"a\"b%%c$$d\\"` {
		t.Errorf("%s", sd(`a"b%c$d\`))
	}
	if _, err := Service(ServiceSpec{Kind: "launchd", Binary: "stoxicity", ConfigDir: "/x", LogDir: "/l"}); err == nil {
		t.Error("absolute paths only")
	}
	if _, err := Service(ServiceSpec{Kind: "cron", Binary: "/a", ConfigDir: "/b"}); err == nil {
		t.Error("kind")
	}
	for _, f := range files {
		if strings.Contains(f.Content, "secret") {
			t.Error("a service file holds no setting values")
		}
	}
}

func TestDrillsRunOnCopiesAndReportMeasurements(t *testing.T) {
	dir := dataFolder(t)
	before := counts(t, dir)
	beforeFiles := listFiles(t, dir)
	work := filepath.Join(t.TempDir(), "work")
	rep, err := RunDrills(context.Background(), dir, work, DefaultTargets())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || rep.DBs != 2 {
		for _, r := range rep.Results {
			t.Logf("%s passed=%v %s", r.Name, r.Passed, r.Detail)
		}
		t.Fatal("a drill failed")
	}
	names := []string{}
	for _, r := range rep.Results {
		names = append(names, r.Name)
		if r.Took <= 0 || len(r.Measures) == 0 {
			t.Errorf("%s measured nothing: %+v", r.Name, r)
		}
	}
	want := "backup verify restore open-and-migrate interrupted-restore interrupted-backup timeouts mirror"
	if strings.Join(names, " ") != want {
		t.Errorf("drills: %v", names)
	}
	if !sameFolder(before, counts(t, dir)) {
		t.Error("the live data was changed")
	}
	if got := listFiles(t, dir); got != beforeFiles {
		t.Errorf("the live folder gained or lost files:\n%s\n%s", beforeFiles, got)
	}
	// a target that cannot be met is reported as failed, with the figure
	rep, err = RunDrills(context.Background(), dir, filepath.Join(t.TempDir(), "w2"), Targets{Backup: time.Nanosecond})
	if err != nil || rep.Passed || rep.Results[0].Name != "backup" || rep.Results[0].Passed || !strings.Contains(rep.Results[0].Detail, "over the target") {
		t.Fatalf("%+v %v", rep.Results[0], err)
	}
	if _, err := RunDrills(context.Background(), dir, filepath.Join(dir, "work"), Targets{}); err == nil {
		t.Error("a drill inside the data folder is refused")
	}
	if _, err := RunDrills(context.Background(), t.TempDir(), t.TempDir(), Targets{}); err == nil {
		t.Error("nothing to drill on")
	}
}

func listFiles(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && !strings.HasSuffix(p, "-shm") && !strings.HasSuffix(p, "-wal") {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return strings.Join(out, "\n")
}

func TestDiskOfFolderThatDoesNotExistYet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "yet")
	if _, err := DiskOf(dir); err != nil {
		t.Fatalf("a folder that does not exist yet has the disk of its parent: %v", err)
	}
	if err := (Quota{}).Check(dir, 0); err != nil {
		t.Fatalf("no limit, no refusal: %v", err)
	}
}
