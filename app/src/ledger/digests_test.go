package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// berlin is a time on the given day of November 2026 at hh:mm in Berlin, which
// is UTC+1 then.
func (f *fixture) berlin(dayOfMonth, hour, minute int) time.Time {
	return time.Date(2026, 11, dayOfMonth, hour, minute, 0, 0, f.zone)
}

func digestWatch(id, cadence, at string, kinds ...channel.Kind) watch.Watch {
	return watch.Watch{ID: id, Name: "Watch " + id, Digest: watch.Digest{Cadence: cadence, At: at, Channels: kinds}}
}

func (f *fixture) digests(t *testing.T, tenantID string) int {
	t.Helper()
	n, err := f.l.Digests(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("Digests: %v", err)
	}
	return n
}

func TestDailyDigestOncePerDay(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", digestWatch("core", "daily", "07:30", channel.File))
	f.save(t,
		article("a1", "pub", "Acme reports annual results", f.berlin(2, 20, 0), f.berlin(2, 20, 5)),
		article("a2", "pub", "Beta raises its dividend", f.berlin(3, 6, 0), f.berlin(3, 6, 5)),
	)

	f.at(f.berlin(3, 7, 29))
	if n := f.digests(t, "alice"); n != 0 {
		t.Errorf("before the digest time: %d sent, want 0", n)
	}
	f.at(f.berlin(3, 7, 30))
	if n := f.digests(t, "alice"); n != 1 {
		t.Fatalf("at the digest time: %d sent, want 1", n)
	}
	got := f.file.notifications()
	if len(got) != 1 {
		t.Fatalf("file channel got %d notifications", len(got))
	}
	n := got[0]
	if n.Alert != "digest" || n.WatchID != "core" || n.TenantID != "alice" || n.Link != "/w/core" ||
		n.Title != "Watch core: daily digest 2026-11-03" || !n.At.Equal(f.berlin(3, 7, 30)) {
		t.Errorf("notification = %+v", n)
	}
	for _, want := range []string{"Digest for 2026-11-02 to 2026-11-03 (Europe/Berlin), tenant alice", "2 articles, 2 relevant", "Acme reports annual results", "Beta raises its dividend"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, n.Body)
		}
	}
	if len(n.Articles) != 2 {
		t.Errorf("Articles = %v", titles(n.Articles))
	}

	f.at(f.berlin(3, 12, 0))
	if n := f.digests(t, "alice"); n != 0 {
		t.Errorf("later the same day: %d sent, want 0", n)
	}
	f.at(f.berlin(4, 7, 29))
	if n := f.digests(t, "alice"); n != 0 {
		t.Errorf("the next day before the time: %d sent, want 0", n)
	}
	f.at(f.berlin(4, 8, 0))
	if n := f.digests(t, "alice"); n != 1 {
		t.Errorf("the next day: %d sent, want 1 (a late start still sends)", n)
	}
	if got := len(f.file.notifications()); got != 2 {
		t.Errorf("file channel got %d notifications, want 2", got)
	}

	// The 24 hours before 08:00 on the 4th hold nothing that was in the first.
	if second := f.file.notifications()[1]; strings.Contains(second.Body, "Acme reports annual results") {
		t.Errorf("the second digest repeats the 20:00 article of the 2nd:\n%s", second.Body)
	}
}

func TestWeeklyDigestOnMondaysOnly(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", digestWatch("core", "weekly", "08:00", channel.File))
	f.save(t, article("a1", "pub", "Acme reports annual results", f.berlin(1, 12, 0), f.berlin(1, 12, 5)))

	// 3 November 2026 is a Tuesday; the 2nd and the 9th are Mondays.
	for _, c := range []struct {
		when time.Time
		want int
		name string
	}{
		{f.berlin(1, 9, 0), 0, "Sunday"},
		{f.berlin(2, 7, 59), 0, "Monday before the time"},
		{f.berlin(2, 8, 0), 1, "Monday at the time"},
		{f.berlin(2, 18, 0), 0, "Monday later"},
		{f.berlin(3, 9, 0), 0, "Tuesday"},
		{f.berlin(8, 9, 0), 0, "the next Sunday"},
		{f.berlin(9, 9, 0), 1, "the next Monday"},
	} {
		f.at(c.when)
		if n := f.digests(t, "alice"); n != c.want {
			t.Errorf("%s (%s): %d sent, want %d", c.name, c.when.Format("Mon 15:04"), n, c.want)
		}
	}
	got := f.file.notifications()
	if len(got) != 2 {
		t.Fatalf("file channel got %d notifications, want 2", len(got))
	}
	if got[0].Title != "Watch core: weekly digest 2026-W45" || got[1].Title != "Watch core: weekly digest 2026-W46" {
		t.Errorf("titles = %q, %q", got[0].Title, got[1].Title)
	}
	if !strings.Contains(got[0].Body, "Acme reports annual results") || strings.Contains(got[1].Body, "Acme reports annual results") {
		t.Errorf("the week of the 2nd holds the article, the week of the 9th does not:\n%s\n---\n%s", got[0].Body, got[1].Body)
	}
}

func TestDigestFailureIsNotRecorded(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", digestWatch("core", "daily", "07:30", channel.File))
	f.at(f.berlin(3, 7, 30))
	f.file.fail = 1

	n, err := f.l.Digests(context.Background(), "alice")
	if err == nil || n != 0 || !strings.Contains(err.Error(), "send to file") {
		t.Fatalf("Digests = %d, %v; want 0 sent and the send error", n, err)
	}
	if sent, _ := f.db.Tenant("alice").DigestSent(context.Background(), "core", "2026-11-03"); sent {
		t.Fatal("a failed digest was recorded as sent")
	}
	f.at(f.berlin(3, 7, 31))
	if n := f.digests(t, "alice"); n != 1 {
		t.Errorf("retry: %d sent, want 1", n)
	}
	if got := f.file.notifications(); len(got) != 1 || !got[0].At.Equal(f.berlin(3, 7, 30)) {
		t.Errorf("sent %+v, want one with the scheduled time 07:30 (stable across attempts)", got)
	}
	if n := f.digests(t, "alice"); n != 0 {
		t.Errorf("after success: %d sent, want 0", n)
	}
}

func TestDigestCadenceOffAndUnknownSendNothing(t *testing.T) {
	f := newFixture(t)
	// The watch store validates on save, so the watches come from a fixed list.
	list := []watch.Watch{
		digestWatch("none", "", "07:30", channel.File),
		digestWatch("off", "off", "07:30", channel.File),
		digestWatch("hourly", "hourly", "07:30", channel.File),
		digestWatch("badtime", "daily", "7am", channel.File),
	}
	f.l.cfg.Watches = func(string) watch.Store { return fixedWatches{list} }
	f.at(f.berlin(3, 12, 0))
	if n := f.digests(t, "alice"); n != 0 {
		t.Errorf("%d sent, want 0", n)
	}
	if got := f.file.notifications(); len(got) != 0 {
		t.Errorf("file channel got %+v", got)
	}
}

type fixedWatches struct{ list []watch.Watch }

func (s fixedWatches) List() ([]watch.Watch, error) { return s.list, nil }
func (s fixedWatches) Get(id string) (watch.Watch, error) {
	for _, w := range s.list {
		if w.ID == id {
			return w, nil
		}
	}
	return watch.Watch{}, errors.New("not found")
}
func (fixedWatches) Save(watch.Watch) error { return nil }
func (fixedWatches) Delete(string) error    { return nil }

func TestDigestUsesOnlyVisibleArticlesOfTheWatch(t *testing.T) {
	f := newFixture(t)
	w := digestWatch("apple", "daily", "07:30", channel.File)
	w.Scope.Tickers = []string{"AAPL"}
	for _, tenantID := range []string{"alice", "bob"} {
		f.saveWatch(t, tenantID, w)
	}
	apple := article("p1", "pub", "Apple public filing", f.berlin(3, 6, 0), f.berlin(3, 6, 5))
	apple.Tickers = []string{"AAPL"}
	internal := article("i1", "int", "Apple internal memo", f.berlin(3, 6, 10), f.berlin(3, 6, 15))
	internal.Tickers = []string{"AAPL"}
	other := article("p2", "pub", "Unrelated filing", f.berlin(3, 6, 20), f.berlin(3, 6, 25))
	other.Tickers = []string{"MSFT"}
	f.save(t, apple, internal, other)
	f.at(f.berlin(3, 7, 30))

	for _, tenantID := range []string{"alice", "bob"} {
		if n := f.digests(t, tenantID); n != 1 {
			t.Fatalf("%s: %d sent, want 1", tenantID, n)
		}
	}
	for _, n := range f.file.notifications() {
		switch n.TenantID {
		case "alice":
			if !strings.Contains(n.Body, "Apple public filing") || !strings.Contains(n.Body, "Apple internal memo") {
				t.Errorf("alice's digest lacks her articles:\n%s", n.Body)
			}
		case "bob":
			if strings.Contains(n.Body, "internal memo") {
				t.Errorf("bob's digest has an invisible article:\n%s", n.Body)
			}
			if !strings.Contains(n.Body, "Apple public filing") {
				t.Errorf("bob's digest lacks the public article:\n%s", n.Body)
			}
		}
		if strings.Contains(n.Body, "Unrelated filing") {
			t.Errorf("%s's digest has an article outside the watch:\n%s", n.TenantID, n.Body)
		}
	}
}

func TestDigestTUIAndUnconfiguredChannels(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", digestWatch("tui", "daily", "07:30", channel.TUI))
	f.saveWatch(t, "alice", digestWatch("mail", "daily", "07:30", channel.Email))
	f.saveWatch(t, "alice", digestWatch("mail2", "daily", "07:30", channel.Email))
	f.at(f.berlin(3, 8, 0))
	if n := f.digests(t, "alice"); n != 0 {
		t.Errorf("%d sent, want 0", n)
	}
	if got := f.file.notifications(); len(got) != 0 {
		t.Errorf("file channel got %+v", got)
	}
	if n := strings.Count(f.logs.String(), "no channel configured"); n != 1 {
		t.Errorf("%d warnings, want 1 for email:\n%s", n, f.logs.String())
	}
	if sent, _ := f.db.Tenant("alice").DigestSent(context.Background(), "tui", "2026-11-03"); !sent {
		t.Error("the TUI digest is not recorded as handled")
	}
	for _, id := range []string{"mail", "mail2"} {
		if sent, _ := f.db.Tenant("alice").DigestSent(context.Background(), id, "2026-11-03"); sent {
			t.Errorf("digest of %s is recorded as sent though no email channel exists", id)
		}
	}
}
