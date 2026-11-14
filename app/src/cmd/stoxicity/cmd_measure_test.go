package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareScratchRefusesTheRealDataFolder(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "data")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{real, filepath.Join(real, "scratch"), root} {
		if _, err := prepareScratch(dir, real); err == nil || !strings.Contains(err.Error(), "overlaps") {
			t.Errorf("%s: want an overlap refusal, got %v", dir, err)
		}
	}
	if _, err := prepareScratch("", real); err == nil {
		t.Error("an empty -scratch must be refused")
	}
}

func TestPrepareScratchNeedsAnEmptyOrOwnFolder(t *testing.T) {
	real := filepath.Join(t.TempDir(), "data")
	busy := t.TempDir()
	if err := os.WriteFile(filepath.Join(busy, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareScratch(busy, real); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("a folder with other files must be refused, got %v", err)
	}

	fresh := filepath.Join(t.TempDir(), "new")
	got, err := prepareScratch(fresh, real)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got, scratchMarker)); err != nil {
		t.Fatalf("no marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(got, "base.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareScratch(got, real); err != nil {
		t.Fatalf("a measurement's own folder may be used again: %v", err)
	}
}

func TestClearScratchKeepsTheReport(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{scratchMarker, "r.md", "r.json", "base.db"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "tenants"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := clearScratch(dir, filepath.Join(dir, "r.md")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != scratchMarker+" r.json r.md" {
		t.Fatalf("left %v", names)
	}
}
