package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Synthetic fixture, not verified against live data.
func sampleNotification() Notification {
	return Notification{
		TenantID: "buffett-core",
		WatchID:  "acme",
		Alert:    "guidance cut",
		Title:    "Acme cuts guidance",
		Body:     "Acme lowered its full-year outlook.\n\nShares fell.",
		Link:     "/w/buffett-core/stock/ACME",
		At:       time.Date(2026, 11, 4, 10, 30, 5, 0, time.UTC),
		Articles: []model.Article{
			{ID: "a1", ClusterID: "c1", URL: "https://news.example.com/acme-cut", Title: "ACME [cut] outlook", Domain: "news.example.com"},
			{ID: "a2", URL: "https://blog.example.org/acme (update)", Title: "Shares fall", Domain: "blog.example.org"},
		},
	}
}

// onlyFile returns the name and content of the single file in dir.
func onlyFile(t *testing.T, dir string) (string, string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 file in %s, got %d", dir, len(entries))
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	return entries[0].Name(), string(data)
}

func TestFileSendContentAndName(t *testing.T) {
	dir := t.TempDir()
	ch, err := NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := sampleNotification()
	if err := ch.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(n.Key()))
	wantName := "20261104-103005-buffett-core-acme-" + hex.EncodeToString(sum[:])[:8] + ".md"
	wantContent := "# Acme cuts guidance\n\n" +
		"- Tenant: buffett-core\n" +
		"- Watch: acme\n" +
		"- Alert: guidance cut\n" +
		"- Time: 2026-11-04T10:30:05Z\n\n" +
		"Acme lowered its full-year outlook.\n\nShares fell.\n\n" +
		"- [ACME \\[cut\\] outlook](https://news.example.com/acme-cut) — news.example.com\n" +
		"- [Shares fall](https://blog.example.org/acme%20%28update%29) — blog.example.org\n"

	name, content := onlyFile(t, dir)
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if content != wantContent {
		t.Errorf("content mismatch\ngot:\n%s\nwant:\n%s", content, wantContent)
	}
}

func TestFileSendResendIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	ch, err := NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := sampleNotification()
	for i := 0; i < 2; i++ {
		if err := ch.Send(context.Background(), n); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	name1, content1 := onlyFile(t, dir)
	if !strings.HasSuffix(name1, ".md") {
		t.Errorf("name %q does not end in .md", name1)
	}
	if err := ch.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	name2, content2 := onlyFile(t, dir)
	if name1 != name2 || content1 != content2 {
		t.Error("resending the same notification changed the file")
	}
}

func TestFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	ch, err := NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %v, want 0700", got)
	}
	if err := ch.Send(context.Background(), sampleNotification()); err != nil {
		t.Fatal(err)
	}
	name, _ := onlyFile(t, dir)
	info, err = os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %v, want 0600", got)
	}
}

func TestFileRejectsEmptyIDs(t *testing.T) {
	cases := []struct {
		name          string
		tenant, watch string
	}{
		{"empty tenant", "", "acme"},
		{"empty watch", "buffett-core", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			ch, err := NewFile(dir)
			if err != nil {
				t.Fatal(err)
			}
			n := sampleNotification()
			n.TenantID, n.WatchID = c.tenant, c.watch
			if err := ch.Send(context.Background(), n); err == nil {
				t.Fatal("expected an error")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("wrote %d files for an invalid notification", len(entries))
			}
		})
	}
}

func TestFileSanitisesNames(t *testing.T) {
	cases := []struct {
		name          string
		tenant, watch string
		wantPart      string
	}{
		{"path separators", "a/../b", "acme", "-a----b-acme-"},
		{"spaces and capitals", "buffett-core", "X Y", "-buffett-core----"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			ch, err := NewFile(dir)
			if err != nil {
				t.Fatal(err)
			}
			n := sampleNotification()
			n.TenantID, n.WatchID = c.tenant, c.watch
			if err := ch.Send(context.Background(), n); err != nil {
				t.Fatal(err)
			}
			name, _ := onlyFile(t, dir)
			if !strings.Contains(name, c.wantPart) {
				t.Errorf("name %q does not contain %q", name, c.wantPart)
			}
		})
	}
}

func TestFileNewFileRequiresDirectory(t *testing.T) {
	if _, err := NewFile(""); err == nil {
		t.Error("expected an error for an empty directory")
	}
}
