package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A database as the previous release left it (migrations 1 to 18, with rows) is
// opened by this one: migrations 19 to 23 add what they add, convert the legacy
// rows the way their comments say, and lose and duplicate nothing.
func TestLegacyRowsSurviveMigrationsNineteenToTwentyThree(t *testing.T) {
	ms, err := loadMigrations(baseFS, "migrations/sqlite/base")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 23 {
		t.Fatalf("%d migrations, want at least 23", len(ms))
	}
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	ctx := context.Background()
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return at }

	if err := migrate(ctx, raw, sqliteDialect{}, ms[:18], now); err != nil {
		t.Fatalf("migrations 1 to 18: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO candle (ticker, day, open, high, low, close, volume) VALUES ('AAA', '2026-11-03', 1, 2, 1, 2, 10), ('AAA', '2026-11-04', 2, 3, 2, 3, 11)`,
		`INSERT INTO split_run (id, since_day, read_ms) VALUES (1, '2025-11-07', 1000)`,
		`INSERT INTO cover_check (cik, checked_ms, accession, problem) VALUES
			('0000000001', 5, '', ''),
			('0000000002', 5, '', 'no annual or quarterly report with a machine-readable cover page in the last 5 reports'),
			('0000000003', 5, '', 'the request failed: timeout')`,
		`INSERT INTO filing_index_day (day, read_ms, listed, missing) VALUES ('2026-11-02', 10, 4, 0), ('2026-11-03', 11, 0, 0)`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("legacy row: %v\n%s", err, q)
		}
	}

	if err := migrate(ctx, raw, sqliteDialect{}, ms, now); err != nil {
		t.Fatalf("migrations 19 to %d: %v", len(ms), err)
	}
	// Running again changes nothing.
	if err := migrate(ctx, raw, sqliteDialect{}, ms, now); err != nil {
		t.Fatalf("second run: %v", err)
	}
	count := func(q string, args ...any) int {
		var n int
		if err := raw.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM candle`); n != 2 {
		t.Errorf("candles = %d, want 2", n)
	}
	if n := count(`SELECT COUNT(*) FROM candle WHERE currency = ''`); n != 2 {
		t.Errorf("legacy candles with an unknown currency = %d, want 2: a currency must not be invented", n)
	}
	if n := count(`SELECT COUNT(*) FROM candle_revision`); n != 0 {
		t.Errorf("candle revisions = %d, want none", n)
	}
	if n := count(`SELECT COUNT(*) FROM split_read WHERE since_day = '2025-11-07' AND read_ms = 1000`); n != 1 {
		t.Errorf("the legacy split reading must be carried over once, got %d", n)
	}
	for cik, want := range map[string]struct {
		kind     string
		failures int
	}{"0000000001": {"", 0}, "0000000002": {"nocover", 0}, "0000000003": {"failed", 1}} {
		var kind string
		var failures int
		if err := raw.QueryRowContext(ctx, `SELECT kind, failures FROM cover_check WHERE cik = ?`, cik).Scan(&kind, &failures); err != nil {
			t.Fatal(err)
		}
		if kind != want.kind || failures != want.failures {
			t.Errorf("cover check %s = %q, %d failures; want %q, %d", cik, kind, failures, want.kind, want.failures)
		}
	}
	if n := count(`SELECT COUNT(*) FROM filing_index_day WHERE forms = ''`); n != 2 {
		t.Errorf("days read before the forms were recorded = %d, want 2 (they are read again when next asked for)", n)
	}
	if n := count(`SELECT COUNT(*) FROM schema_migrations`); n != len(ms) {
		t.Errorf("%d migrations recorded, want %d, each once", n, len(ms))
	}
}
