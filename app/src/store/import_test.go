package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// The importer copies a SQLite store into an empty Postgres database and proves
// the copy by reading both back. It needs a Postgres to run against.
func TestImportSQLiteCopiesEverythingAndProvesIt(t *testing.T) {
	if pgTestURL() == "" {
		t.Skip("needs STOXICITY_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	dir := t.TempDir()
	src, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	src.now = clock(pfT0)
	base := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	var arts []model.Article
	for i := 0; i < 50; i++ {
		a := art("a"+string(rune('a'+i%26))+string(rune('a'+i/26)), base.Add(time.Duration(i)*time.Minute))
		a.Title = "Ünïcode title " + a.ID + " \"quoted\" 'single'"
		arts = append(arts, a)
	}
	if err := src.Save(ctx, arts); err != nil {
		t.Fatal(err)
	}
	acme := src.Tenant("acme")
	if err := acme.Labels().Put(ctx, labels.Label{ArticleID: "x", Verdict: labels.Keep, At: base, LensVersion: "v1", Sampled: true}); err != nil {
		t.Fatal(err)
	}
	if err := acme.PutHits(ctx, []watch.Hit{{WatchID: "w1", ArticleID: "x", At: base, Alerts: []string{"big"}}}); err != nil {
		t.Fatal(err)
	}
	p := src.Portfolios("default")
	mkBook(t, p, "main", "core", portfolio.Real)
	deposit(t, p, "main/core", 5_000, "d1")
	deposit(t, p, "main/core", 250, "d2")
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	before := hashTree(t, dir)

	dst := newPGDB(t)
	rep, err := dst.ImportSQLite(ctx, dir, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Schemas) < 3 { // base, tenant_acme, tenant_default
		t.Fatalf("schemas: %+v", rep.Schemas)
	}
	// The source files are untouched.
	if after := hashTree(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("the import changed the source files:\nbefore %v\nafter  %v", before, after)
	}
	// The copy is usable: the data reads back and new writes continue the sequences.
	got, err := dst.Range(ctx, base, base.Add(24*time.Hour))
	if err != nil || len(got) != 50 {
		t.Fatalf("articles: %d %v", len(got), err)
	}
	more := art("zz-new", base.Add(time.Hour))
	if err := dst.Save(ctx, []model.Article{more}); err != nil {
		t.Fatal(err)
	}
	deposit(t, dst.Portfolios("default"), "main/core", 1, "d3")
	l, err := dst.Portfolios("default").Ledger(ctx, "main/core")
	if err != nil || l.Projection().Cash("EUR") != 5_251 {
		t.Fatalf("cash after import: %v %v", l, err)
	}
	// A second import into the same database is refused.
	if _, err := dst.ImportSQLite(ctx, dir, nil); err == nil {
		t.Fatal("imported into a database that holds rows")
	}
}

func TestImportSQLiteRefusesAnOlderSchema(t *testing.T) {
	if pgTestURL() == "" {
		t.Skip("needs STOXICITY_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	dir := t.TempDir()
	list, err := loadMigrations(baseFS, "migrations/sqlite/base")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dir, "base.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, raw, sqliteDialect{}, list[:10], time.Now); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	dst := newPGDB(t)
	if _, err := dst.ImportSQLite(ctx, dir, nil); err == nil {
		t.Fatal("imported an older schema")
	}
}

func TestImportSQLiteIsNotForSQLite(t *testing.T) {
	db, _ := openTemp(t)
	if db.pgURL != "" {
		t.Skip()
	}
	if _, err := db.ImportSQLite(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("import into SQLite")
	}
}

// hashTree maps every database file under dir to the hash of its content. The
// -wal and -shm files SQLite creates beside a database it reads are left out:
// they hold no data of their own when the database was closed cleanly.
func hashTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".db" {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
