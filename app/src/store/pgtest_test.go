package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// With STOXICITY_TEST_DATABASE_URL set to the URL of a database a role that may
// create databases can connect to (for example postgresql://user@localhost/postgres),
// the tests that use openTemp run on Postgres: each gets a database of its own,
// created with the byte-order locale and dropped when the test ends. Without it
// they run on SQLite, as before. The tests that look at SQLite's files call
// sqliteOnly.
func pgTestURL() string { return os.Getenv("STOXICITY_TEST_DATABASE_URL") }

// reopenTemp closes db and opens the same store again.
func reopenTemp(t *testing.T, db *DB, dir string) *DB {
	t.Helper()
	url := db.pgURL
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var db2 *DB
	var err error
	if url != "" {
		db2, err = OpenPostgres(context.Background(), url)
	} else {
		db2, err = Open(dir)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	return db2
}

// sqliteOnly skips a test that is about SQLite itself when the suite runs on Postgres.
func sqliteOnly(t *testing.T) {
	t.Helper()
	if pgTestURL() != "" {
		t.Skip("about SQLite itself")
	}
}

func newPGDB(t *testing.T) *DB {
	t.Helper()
	admin := pgTestURL()
	var b [6]byte
	rand.Read(b[:])
	name := "stoxicity_t_" + hex.EncodeToString(b[:])
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()+
		` TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER builtin BUILTIN_LOCALE 'C.UTF-8'`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := OpenPostgres(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	})
	return db
}

// openAgain opens a second handle on the store of db, as another process would.
func openAgain(t *testing.T, db *DB, dir string) *DB {
	t.Helper()
	var db2 *DB
	var err error
	if db.pgURL != "" {
		db2, err = OpenPostgres(context.Background(), db.pgURL)
	} else {
		db2, err = Open(dir)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	return db2
}
