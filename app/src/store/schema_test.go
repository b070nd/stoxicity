package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestKnownSchemaMatchesTheEmbeddedFiles(t *testing.T) {
	k, err := KnownSchema()
	if err != nil {
		t.Fatal(err)
	}
	if k.Base < 23 || k.Tenant < 4 {
		t.Errorf("known schema %+v is below what the tree has", k)
	}
}

func TestReadSchemaTouchesNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")

	got, err := ReadSchema(ctx, path)
	if err != nil || got.Exists {
		t.Fatalf("missing file: %+v, %v", got, err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("ReadSchema created the file")
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_ms INTEGER NOT NULL);
		INSERT INTO schema_migrations VALUES (1, 'a', 0), (2, 'b', 0), (7, 'c', 0)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	before, _ := os.ReadFile(path)

	got, err = ReadSchema(ctx, path)
	if err != nil || !got.Exists || got.Applied != 7 {
		t.Fatalf("existing file: %+v, %v", got, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("ReadSchema changed the database file")
	}

	empty := filepath.Join(dir, "empty.db")
	raw, _ = sql.Open("sqlite", empty)
	raw.Exec(`CREATE TABLE t (x)`)
	raw.Close()
	if got, err := ReadSchema(ctx, empty); err != nil || !got.Exists || got.Applied != 0 {
		t.Errorf("no migration table: %+v, %v", got, err)
	}
}
