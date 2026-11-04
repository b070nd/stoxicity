package channel

// Synthetic fixtures, not verified against live data.

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// readFeed is what a feed reader sees; it is decoded independently of the
// types the package writes with.
type readFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Entries []readEntry `xml:"entry"`
}

type readEntry struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Summary string `xml:"summary"`
	Links   []struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

func readFeedFile(t *testing.T, path string) readFeed {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f readFeed
	if err := xml.Unmarshal(data, &f); err != nil {
		t.Fatalf("feed does not parse: %v", err)
	}
	return f
}

// feedNote builds a notification whose Key depends only on its title.
func feedNote(title, body string, at time.Time) Notification {
	return Notification{
		TenantID: "buffett-core",
		WatchID:  "acme",
		Alert:    "guidance",
		Title:    title,
		Body:     body,
		At:       at,
		Articles: []model.Article{{ID: title, URL: "https://news.example.com/" + title, Title: title}},
	}
}

func titles(f readFeed) []string {
	out := make([]string, 0, len(f.Entries))
	for _, e := range f.Entries {
		out = append(out, e.Title)
	}
	return out
}

var feedAt = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

func TestFeedNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.xml")
	ch, err := NewFeed(path, "Acme news", "https://feeds.example.com/acme.xml", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"A", "B", "C"} {
		if err := ch.Send(context.Background(), feedNote(title, "body "+title, feedAt)); err != nil {
			t.Fatal(err)
		}
	}
	f := readFeedFile(t, path)
	if got := titles(f); fmt.Sprint(got) != "[C B A]" {
		t.Errorf("order = %v, want [C B A]", got)
	}
	if f.Entries[0].Summary != "body C" {
		t.Errorf("summary = %q", f.Entries[0].Summary)
	}
	if len(f.Entries[0].Links) != 1 || f.Entries[0].Links[0].Href != "https://news.example.com/C" {
		t.Errorf("entry links = %+v, want one link to the article", f.Entries[0].Links)
	}
}

func TestFeedReplacesSameID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.xml")
	ch, err := NewFeed(path, "Acme news", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := ch.Send(ctx, feedNote("A", "v1", feedAt)); err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(ctx, feedNote("B", "body", feedAt)); err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(ctx, feedNote("A", "v2", feedAt)); err != nil {
		t.Fatal(err)
	}
	f := readFeedFile(t, path)
	if got := titles(f); fmt.Sprint(got) != "[A B]" {
		t.Fatalf("titles = %v, want [A B]: the replaced entry must not repeat", got)
	}
	if f.Entries[0].Summary != "v2" {
		t.Errorf("replaced entry summary = %q, want v2", f.Entries[0].Summary)
	}
}

func TestFeedMaxDropsOldest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.xml")
	ch, err := NewFeed(path, "Acme news", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"A", "B", "C"} {
		if err := ch.Send(context.Background(), feedNote(title, "", feedAt)); err != nil {
			t.Fatal(err)
		}
	}
	if got := titles(readFeedFile(t, path)); fmt.Sprint(got) != "[C B]" {
		t.Errorf("titles = %v, want [C B]", got)
	}
}

func TestFeedDefaultMaxIs100(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.xml")
	ch, err := NewFeed(path, "Acme news", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		if err := ch.Send(context.Background(), feedNote(fmt.Sprintf("n%03d", i), "", feedAt)); err != nil {
			t.Fatal(err)
		}
	}
	f := readFeedFile(t, path)
	if len(f.Entries) != 100 {
		t.Fatalf("entries = %d, want 100", len(f.Entries))
	}
	if f.Entries[0].Title != "n100" || f.Entries[99].Title != "n001" {
		t.Errorf("kept %s..%s, want n100..n001", f.Entries[0].Title, f.Entries[99].Title)
	}
}

func TestFeedEntryIDFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.xml")
	ch, err := NewFeed(path, "Acme news", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(context.Background(), feedNote("A", "", feedAt)); err != nil {
		t.Fatal(err)
	}
	id := readFeedFile(t, path).Entries[0].ID
	want := regexp.MustCompile(`^tag:stoxicity,2026-11-04:[0-9a-f]{64}$`)
	if !want.MatchString(id) {
		t.Errorf("id = %q, want tag:stoxicity,<date>:<64 hex>", id)
	}
}

func TestFeedMalformedExistingFileIsNotOverwritten(t *testing.T) {
	cases := []struct {
		name, content string
	}{
		{"not xml", "this is not xml at all"},
		{"other root element", `<rss version="2.0"><channel/></rss>`},
		{"truncated", `<feed xmlns="http://www.w3.org/2005/Atom"><entry>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "feed.xml")
			if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			ch, err := NewFeed(path, "Acme news", "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := ch.Send(context.Background(), feedNote("A", "", feedAt)); err == nil {
				t.Fatal("expected an error for a malformed feed")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != c.content {
				t.Error("malformed file was overwritten")
			}
		})
	}
}

func TestFeedConcurrentSends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.xml")
	ch, err := NewFeed(path, "Acme news", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- ch.Send(context.Background(), feedNote(fmt.Sprintf("c%02d", i), "", feedAt))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	f := readFeedFile(t, path)
	if len(f.Entries) != n {
		t.Fatalf("entries = %d, want %d", len(f.Entries), n)
	}
	seen := map[string]bool{}
	for _, e := range f.Entries {
		if seen[e.ID] {
			t.Errorf("duplicate entry %s", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestFilePermissionsForFeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.xml")
	ch, err := NewFeed(path, "Acme news", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(context.Background(), feedNote("A", "", feedAt)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("feed mode = %v, want 0600", got)
	}
}

func TestNewFeedRequiresPathAndTitle(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewFeed("", "Acme news", "", 0); err == nil {
		t.Error("expected an error for an empty path")
	}
	if _, err := NewFeed(filepath.Join(dir, "feed.xml"), "", "", 0); err == nil {
		t.Error("expected an error for an empty title")
	}
}
