package ops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

// These tests need a Postgres server (STOXICITY_TEST_DATABASE_URL, a role that
// may create databases) and the pg_dump and pg_restore tools.
func pgOpsEnv(t *testing.T) (base string) {
	t.Helper()
	base = os.Getenv("STOXICITY_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("needs STOXICITY_TEST_DATABASE_URL")
	}
	if _, err := pgTool("pg_dump"); err != nil {
		t.Skip(err)
	}
	return base
}

func TestPostgresBackupVerifyAndRestore(t *testing.T) {
	admin := pgOpsEnv(t)
	ctx := context.Background()
	live, err := newScratchDB(ctx, admin, "stoxicity_t_ops_")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { live.drop() })

	db, err := store.OpenPostgres(ctx, live.URL)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	var arts []model.Article
	for _, id := range []string{"a1", "a2", "a3"} {
		arts = append(arts, model.Article{ID: id, SourceID: "src", Domain: "example.com", URL: "https://example.com/" + id,
			Title: "Ünïcode " + id, Summary: "s", Published: day, Fetched: day.Add(time.Minute)})
	}
	if err := db.Save(ctx, arts); err != nil {
		t.Fatal(err)
	}
	if err := db.Tenant("acme").Labels().Put(ctx, labels.Label{ArticleID: "a1", Verdict: labels.Keep, At: day, LensVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "company"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "company", "x.txt"), []byte("filing"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dest, m, err := SnapshotPostgres(ctx, live.URL, dataDir, root, SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Coherent || len(m.Files) != 2 {
		t.Fatalf("manifest: %+v", m)
	}
	var dump FileEntry
	for _, f := range m.Files {
		if f.Kind == pgDumpKind {
			dump = f
		}
	}
	if dump.Tables["base.articles"] != 3 || dump.Tables["tenant_acme.labels"] != 1 || dump.Schemas["base"] == 0 {
		t.Fatalf("recorded rows %v schemas %v", dump.Tables, dump.Schemas)
	}
	if strings.Contains(string(mustRead(t, filepath.Join(dest, "MANIFEST.json"))), "postgresql://") {
		t.Fatal("the manifest holds a database URL")
	}

	// deep: the archive is restored into a scratch database and counted
	rep, err := VerifyWith(ctx, dest, VerifyOptions{PostgresURL: live.URL})
	if err != nil || !rep.OK || len(rep.Notes) != 0 {
		t.Fatalf("deep verify: %+v %v", rep, err)
	}
	// shallow: checksums and listing, and it says the rows were not checked
	rep, err = Verify(ctx, dest)
	if err != nil || !rep.OK || len(rep.Notes) != 1 {
		t.Fatalf("shallow verify: %+v %v", rep, err)
	}

	// a damaged archive does not verify
	bad := filepath.Join(t.TempDir(), filepath.Base(dest))
	if err := copyTree(ctx, dest, bad); err != nil {
		t.Fatal(err)
	}
	raw := mustRead(t, filepath.Join(bad, PGDumpFile))
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(filepath.Join(bad, PGDumpFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if rep, err := VerifyWith(ctx, bad, VerifyOptions{PostgresURL: live.URL}); err != nil || rep.OK {
		t.Fatalf("a damaged archive verified: %+v %v", rep, err)
	}

	// a backup of a missing database is refused and leaves nothing behind
	if _, _, err := SnapshotPostgres(ctx, strings.Replace(live.URL, live.name, "no_such_database_here", 1), dataDir, t.TempDir(), SnapshotOptions{}); err == nil {
		t.Fatal("backed up a database that does not exist")
	}

	// restore goes into a new database and leaves the live one alone
	res, err := RestorePostgres(ctx, dest, live.URL, RestoreOptions{Now: func() time.Time { return day }})
	if err != nil {
		t.Fatal(err)
	}
	restored := &scratchDB{admin: live.admin, name: res.Database}
	t.Cleanup(func() { restored.drop() })
	if res.Database == live.name || res.Previous != live.name {
		t.Fatalf("restore: %+v", res)
	}
	got, err := store.InspectPostgres(ctx, res.URL, true)
	if err != nil || got.Tables["base.articles"] != 3 || got.Tables["tenant_acme.labels"] != 1 {
		t.Fatalf("restored rows %v %v", got.Tables, err)
	}
	// the restored database opens and reads
	rdb, err := store.OpenPostgres(ctx, res.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	if a, err := rdb.Range(ctx, day.Add(-time.Hour), day.Add(time.Hour)); err != nil || len(a) != 3 {
		t.Fatalf("restored articles: %d %v", len(a), err)
	}

	// health reads the schemas without writing and does not show the URL
	h, err := Check(ctx, HealthOptions{DataDir: dataDir, PostgresURL: live.URL, BackupRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if h.Status == Fail || len(h.DBs) != 2 || h.DBs[0].Name != "base" || h.DBs[0].State != "current" || h.Backup.Count != 1 {
		t.Fatalf("health: %+v", h)
	}
	out, _ := json.Marshal(h)
	if strings.Contains(string(out), "postgres") && strings.Contains(string(out), live.name) {
		t.Fatalf("health names the database: %s", out)
	}
	// an unreachable server is a failure, not a quiet pass
	h, err = Check(ctx, HealthOptions{DataDir: dataDir, PostgresURL: "postgresql://nobody@127.0.0.1:1/none", BackupRoot: root})
	if err != nil || h.Status != Fail {
		t.Fatalf("unreachable: %+v %v", h, err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPGConnKeepsThePasswordOffTheCommandLine(t *testing.T) {
	dsn, env, err := pgConn("postgresql://user:s3cret@db.example:5432/app?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dsn, "s3cret") || len(env) != 1 || env[0] != "PGPASSWORD=s3cret" {
		t.Fatalf("%q %v", dsn, env)
	}
	if !strings.Contains(dsn, "user@db.example:5432/app") {
		t.Fatalf("%q", dsn)
	}
}
