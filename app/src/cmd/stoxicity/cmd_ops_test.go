package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/ops"
)

var backupIDRE = regexp.MustCompile(`\d{8}T\d{6}Z`)

// fundedEnv has a data folder with a real book and some cash.
func fundedEnv(t *testing.T) *testEnv {
	t.Helper()
	restoreSkipProcessCheck = true
	t.Cleanup(func() { restoreSkipProcessCheck = false })
	e := newTestEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	return e
}

// runAt runs the program with the clock at another time.
func (e *testEnv) runAt(t *testing.T, at time.Time, args ...string) result {
	t.Helper()
	return runWith(t, runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return at }},
		append([]string{"-config-dir", e.dir}, args...)...)
}

func TestOpsHealthIsRedactedAndDoesNotOpenAnyDatabase(t *testing.T) {
	e := fundedEnv(t)
	r := e.run(t, "ops", "health")
	r.wantCode(t, exitOK)
	r.wantOut(t, "health: WARN", "base", "tenant-1", "current", "backups: none", "no backup", "no database was opened")
	js := e.run(t, "ops", "health", "-json")
	js.wantCode(t, exitOK)
	var h ops.Health
	if err := json.Unmarshal([]byte(js.out), &h); err != nil || h.Status != ops.Warn || len(h.DBs) != 2 {
		t.Fatalf("%v %+v", err, h)
	}
	for _, leak := range []string{e.dir, "default.db"} {
		if strings.Contains(js.out, leak) {
			t.Errorf("health leaks %q", leak)
		}
	}
	m := e.run(t, "ops", "metrics")
	m.wantCode(t, exitOK)
	m.wantOut(t, "stoxicity_health_status 1", `stoxicity_db_bytes{db="base"}`)
	out := filepath.Join(e.dir, "metrics.prom")
	e.run(t, "ops", "metrics", "-out", out).wantCode(t, exitOK)
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "stoxicity_disk_free_bytes") {
		t.Errorf("%v", err)
	}
}

func TestOpsBackupMirrorRotateAndVerify(t *testing.T) {
	e := fundedEnv(t)
	root, mirror := filepath.Join(e.dir, "bk"), filepath.Join(e.dir, "mirror")
	var ids []string
	for i := 0; i < 3; i++ {
		at := testNow.Add(time.Duration(i) * time.Hour)
		r := e.runAt(t, at, "ops", "backup", "-to", root, "-mirror", mirror, "-keep", "2")
		r.wantCode(t, exitOK)
		r.wantOut(t, "verified (checksums, integrity, rows of every table)", "mirror: copied and verified")
		ids = append(ids, backupIDRE.FindString(r.out))
		if i == 2 {
			r.wantOut(t, "removed 1 older backup")
		}
	}
	r := e.run(t, "ops", "list", "-to", root)
	r.wantOut(t, ids[1], ids[2], "yes")
	r.wantNotOut(t, ids[0])
	// the mirror keeps all three: rotation never touches the second copy
	if ents, _ := os.ReadDir(mirror); len(ents) != 3 {
		t.Errorf("the mirror holds %d backups", len(ents))
	}
	v := e.run(t, "ops", "verify", filepath.Join(root, ids[2]))
	v.wantCode(t, exitOK)
	v.wantOut(t, "is intact")
	// damage: a changed file and a file nobody listed
	if err := os.WriteFile(filepath.Join(root, ids[2], "tenants", "default.db"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ids[2], "stray.txt"), []byte("x"), 0o644)
	v = e.run(t, "ops", "verify", filepath.Join(root, ids[2]))
	v.wantCode(t, exitError)
	v.wantOut(t, "does NOT verify", "tenants/default.db: the file differs from its checksum", "stray.txt: not in the manifest")
	// a restore of a damaged backup changes nothing
	r = e.run(t, "ops", "restore", filepath.Join(root, ids[2]), "-replace-data", "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "does not verify, nothing was changed")
	e.run(t, "portfolio", "show", "main/core").wantOut(t, "1000.00")
}

func TestOpsRestoreHoldsBackNewOrdersUntilTheBrokerIsReconciled(t *testing.T) {
	e := fundedEnv(t)
	root := filepath.Join(e.dir, "bk")
	e.run(t, "ops", "backup", "-to", root).wantCode(t, exitOK)
	id := backupIDRE.FindString(e.run(t, "ops", "list", "-to", root).out)
	// money moves after the backup
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-03", "-amount", "500.00 EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "show", "main/core").wantOut(t, "1500.00")

	e.run(t, "ops", "restore", filepath.Join(root, id)).wantCode(t, exitUsage)
	e.run(t, "ops", "restore", filepath.Join(root, id), "-replace-data").wantCode(t, exitUsage)
	r := e.run(t, "ops", "restore", filepath.Join(root, id), "-replace-data", "-by", "me")
	r.wantCode(t, exitOK)
	r.wantOut(t, "restored backup "+id, "nothing was deleted", "switched off in 1 tenant", "ops reconciled")
	if ents, _ := filepath.Glob(filepath.Join(e.dir, "data.before-restore-*")); len(ents) != 1 {
		t.Fatalf("the replaced folder is kept: %v", ents)
	}
	sh := e.run(t, "portfolio", "show", "main/core")
	sh.wantOut(t, "1000.00")
	sh.wantNotOut(t, "1500.00")

	e.run(t, "order", "control", "show").wantOut(t, "DISABLED", "restored from backup "+id)
	h := e.run(t, "ops", "health")
	h.wantOut(t, "not reconciled with the broker")

	// reconciling clears the hold on new risk (no book is bound to a broker here)
	r = e.run(t, "ops", "reconciled", "-by", "me", "-note", "no broker in use")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no book is bound to a broker", "order control enable")
	e.run(t, "ops", "reconciled", "-by", "me").wantOut(t, "nothing to reconcile")
	mk, ok, err := ops.ReadRestore(filepath.Join(e.dir, "data"))
	if err != nil || !ok || mk.State != ops.Reconciled || mk.ClearedBy != "me" || mk.BackupID != id {
		t.Fatalf("%+v %v %v", mk, ok, err)
	}
	// the manifest of the backup travels into the restored folder
	if _, err := os.Stat(filepath.Join(e.dir, "data", "restored-from", "MANIFEST.json")); err != nil {
		t.Error(err)
	}
}

func TestRestoredFolderRefusesNewRiskThroughTheSharedGate(t *testing.T) {
	e := fundedEnv(t)
	root := filepath.Join(e.dir, "bk")
	e.run(t, "ops", "backup", "-to", root).wantCode(t, exitOK)
	id := backupIDRE.FindString(e.run(t, "ops", "list", "-to", root).out)
	e.run(t, "ops", "restore", filepath.Join(root, id), "-replace-data", "-by", "me").wantCode(t, exitOK)
	src, _ := workflowOf(t, e)
	src.DataDir = filepath.Join(e.dir, "data")
	g, err := src.Gate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Suspended) != 1 || !strings.Contains(g.Suspended[0], "has not been reconciled") {
		t.Fatalf("%+v", g.Suspended)
	}
	src.DataDir = ""
	if g, _ := src.Gate(t.Context()); len(g.Suspended) != 0 {
		t.Errorf("no data folder given, no restore reason: %+v", g.Suspended)
	}
}

func TestOpsDrillAndService(t *testing.T) {
	e := fundedEnv(t)
	r := e.run(t, "ops", "drill", "-json")
	r.wantCode(t, exitOK)
	var rep ops.DrillReport
	if err := json.Unmarshal([]byte(r.out), &rep); err != nil || !rep.Passed || len(rep.Results) != 8 {
		t.Fatalf("%v\n%s", err, r.out)
	}
	r = e.run(t, "ops", "drill", "-target-backup", "1ns")
	r.wantCode(t, exitError)
	r.wantOut(t, "FAIL", "over the target", "Run on copies")
	e.run(t, "ops", "drill", "-work", filepath.Join(e.dir, "data", "w")).wantCode(t, exitError)

	r = e.run(t, "ops", "service", "launchd", "-logs", filepath.Join(e.dir, "logs"))
	r.wantCode(t, exitOK)
	r.wantOut(t, "dev.stoxicity.alerts", "dev.stoxicity.backup", "<string>scan</string>", "Nothing was installed or started")
	out := filepath.Join(e.dir, "svc")
	e.run(t, "ops", "service", "systemd", "-out", out, "-env-file", filepath.Join(e.dir, ".env")).wantCode(t, exitOK)
	for _, n := range []string{"stoxicity-alerts.service", "stoxicity-alerts.timer", "stoxicity-backup.service", "stoxicity-backup.timer"} {
		if _, err := os.Stat(filepath.Join(out, n)); err != nil {
			t.Error(err)
		}
	}
	e.run(t, "ops", "service", "systemd", "-out", out).wantCode(t, exitError) // existing files are not replaced
	e.run(t, "ops", "service", "cron").wantCode(t, exitUsage)
}

func TestLeaseStopsASecondDeliveryAndDiskLimitsStopHeavyWorkOnly(t *testing.T) {
	e := fundedEnv(t)
	ls := ops.Leases{Dir: filepath.Join(e.dir, "data", "leases"), Now: func() time.Time { return testNow }}
	h, err := ls.Acquire("alerts-deliver-default", "pid 1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := e.run(t, "alerts", "deliver")
	r.wantCode(t, exitError)
	r.wantErr(t, "another delivery is running", "pid 1")
	e.run(t, "ops", "leases").wantOut(t, "alerts-deliver-default", "held")
	h.Release()
	e.run(t, "alerts", "deliver").wantCode(t, exitOK)
	e.run(t, "ops", "leases").wantOut(t, "expired")

	// a full disk (here: a limit nobody can meet) stops fetching and backups, not alerts or the books
	t.Setenv("STOXICITY_MIN_FREE_MB", "999999999999")
	r = e.run(t, "fetch")
	r.wantCode(t, exitError)
	r.wantErr(t, "nothing was fetched", "must stay free")
	r = e.run(t, "ops", "backup", "-to", filepath.Join(e.dir, "bk"))
	r.wantCode(t, exitError)
	r.wantErr(t, "no backup was made")
	e.run(t, "ops", "health").wantCode(t, exitError)
	e.run(t, "alerts", "deliver").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-03", "-amount", "1.00 EUR").wantCode(t, exitOK)
	t.Setenv("STOXICITY_MIN_FREE_MB", "lots")
	e.run(t, "ops", "health").wantCode(t, exitUsage)
}
