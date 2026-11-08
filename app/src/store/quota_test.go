package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/quota"
)

func TestQuotaIsAtomicAcrossConnectionsAndRestarts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	limit := quota.Limit{Requests: 5, Period: time.Hour}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := a
			if i%2 == 0 {
				db = b
			}
			err := db.TakeQuota(ctx, "provider.test", limit, now)
			var full *quota.Exhausted
			if err == nil {
				successes.Add(1)
			} else if !errors.As(err, &full) {
				t.Errorf("reserve: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 5 {
		t.Fatalf("%d requests allowed", successes.Load())
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.TakeQuota(ctx, "provider.test", limit, now.Add(-time.Hour)); err == nil {
		t.Fatal("clock rollback refilled budget")
	}
	if err := c.TakeQuota(ctx, "provider.test", limit, now.Add(time.Minute)); err == nil {
		t.Fatal("restart refilled budget")
	}
	if err := c.TakeQuota(ctx, "other.test", limit, now); err != nil {
		t.Fatal(err)
	}
	if err := c.TakeQuota(ctx, "provider.test", limit, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaCooldownSurvivesBudgetResetAndConfigChange(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	if err := db.TakeQuota(ctx, "p", quota.Limit{Requests: 1, Period: time.Minute}, now); err != nil {
		t.Fatal(err)
	}
	until := now.Add(24 * time.Hour)
	if err := db.BlockQuota(ctx, "p", until, 429); err != nil {
		t.Fatal(err)
	}
	if err := db.BlockQuota(ctx, "p", now.Add(time.Minute), 429); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []quota.Limit{{}, {Requests: 100, Period: time.Hour}} {
		err := db.TakeQuota(ctx, "p", limit, now.Add(2*time.Hour))
		var full *quota.Exhausted
		if !errors.As(err, &full) || !full.Until.Equal(until) {
			t.Errorf("cooldown lost: %v", err)
		}
	}
	if err := db.TakeQuota(ctx, "p", quota.Limit{}, until); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaMigrationOnCopyPreservesLegacyRows(t *testing.T) {
	ctx := context.Background()
	migrations, err := loadMigrations(baseFS, "migrations/base")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "before.db")
	raw, err := sql.Open("sqlite", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, raw, migrations[:26], time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO candle(ticker,day,open,high,low,close,volume) VALUES('KEEP','2026-11-03',1,2,1,2,5)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	copyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(copyDir, "base.db"), body, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(copyDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.base.r.QueryRow(`SELECT COUNT(*) FROM candle WHERE ticker='KEEP' AND close=2`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy row: %d %v", count, err)
	}
	if err := db.TakeQuota(ctx, "test", quota.Limit{Requests: 1, Period: time.Hour}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The source fixture was not migrated or changed by migrating the copy.
	after, err := os.ReadFile(oldPath)
	if err != nil || string(after) != string(body) {
		t.Fatal("source fixture changed")
	}
}

func TestQuotaExtendedExhaustedWindowSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	if err := db.TakeQuota(ctx, "p", quota.Limit{Requests: 1, Period: time.Hour}, now); err != nil {
		t.Fatal(err)
	}
	limit := quota.Limit{Requests: 1, Period: 2 * time.Hour}
	until := now.Add(130 * time.Minute)
	err = db.TakeQuota(ctx, "p", limit, now.Add(10*time.Minute))
	var full *quota.Exhausted
	if !errors.As(err, &full) || !full.Until.Equal(until) {
		t.Fatalf("extended refusal: %v", err)
	}
	states, err := db.QuotaStates(ctx)
	if err != nil || len(states) != 1 || !states[0].Reset.Equal(until) {
		t.Fatalf("not persisted: %+v %v", states, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, config := range []quota.Limit{limit, {Requests: 1, Period: time.Minute}} {
		err := db.TakeQuota(ctx, "p", config, now.Add(time.Hour))
		if !errors.As(err, &full) || !full.Until.Equal(until) {
			t.Fatalf("extended window lost: %v", err)
		}
	}
	if err := db.TakeQuota(ctx, "p", quota.Limit{Requests: 1, Period: time.Minute}, until); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaDeadlinesNeverRoundDown(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 10, 0, 0, 500000, time.UTC)
	limit := quota.Limit{Requests: 1, Period: time.Millisecond}
	if err := db.TakeQuota(ctx, "budget", limit, now); err != nil {
		t.Fatal(err)
	}
	if err := db.TakeQuota(ctx, "budget", limit, now.Add(time.Millisecond)); err == nil {
		t.Fatal("window rounded down")
	}
	until := now.Add(time.Millisecond)
	if err := db.BlockQuota(ctx, "throttle", until, 429); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckQuotaCooldown(ctx, "throttle", until); err == nil {
		t.Fatal("cooldown rounded down")
	}
}
