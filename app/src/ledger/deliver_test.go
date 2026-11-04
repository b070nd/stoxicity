package ledger

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/watch"
)

func TestDeliverGroupsPerWatchAndAlert(t *testing.T) {
	f := newFixture(t)
	big := alert("big", channel.File)
	big.When.MinMateriality = 4
	f.saveWatch(t, "alice", alertWatch("core", big, alert("any", channel.File, channel.Webhook)))
	f.saveWatch(t, "alice", alertWatch("other", alert("any", channel.File)))
	a1 := article("a1", "pub", "Acme announces a buyback", day(9, 0), day(10, 0))
	a1.Score.Materiality = 5
	a2 := article("a2", "pub", "Acme reports annual results", day(9, 30), day(10, 5))
	a3 := article("a3", "pub", "Acme appoints a director", day(9, 45), day(10, 10))
	f.save(t, a1, a2, a3)
	f.at(day(10, 30))
	f.evaluate(t, "alice")

	d := f.deliver(t, "alice")
	if want := (Delivery{Sent: 3}); d != want {
		t.Fatalf("Deliver = %+v, want %+v", d, want)
	}
	byKey := map[string]channel.Notification{}
	for _, n := range f.file.notifications() {
		byKey[n.WatchID+"/"+n.Alert] = n
	}
	if len(byKey) != 3 {
		t.Fatalf("file channel got %d notifications, want 3 (core/big, core/any, other/any): %v", len(byKey), byKey)
	}
	any := byKey["core/any"]
	if any.Title != "Watch core: any (3)" || any.Link != "/w/core" || any.TenantID != "alice" || any.WatchID != "core" || any.Alert != "any" {
		t.Errorf("core/any = %+v", any)
	}
	if !reflect.DeepEqual(titles(any.Articles), []string{"Acme announces a buyback", "Acme reports annual results", "Acme appoints a director"}) {
		t.Errorf("core/any articles = %v", titles(any.Articles))
	}
	if any.Articles[0].URL != "https://pub.example/a1" || any.Articles[0].Score.Materiality != 5 {
		t.Errorf("the notification does not carry the full article: %+v", any.Articles[0])
	}
	for _, want := range []string{"- Acme announces a buyback (pub.example, buyback, materiality 5)\n", "- Acme appoints a director"} {
		if !strings.Contains(any.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, any.Body)
		}
	}
	if !any.At.Equal(day(10, 30)) {
		t.Errorf("At = %v, want the detection time 10:30", any.At)
	}
	if big := byKey["core/big"]; big.Title != "Watch core: big (1)" || len(big.Articles) != 1 || big.Articles[0].ID != "a1" {
		t.Errorf("core/big = %+v", big)
	}
	if got := f.webhook.notifications(); len(got) != 1 || got[0].Alert != "any" || got[0].WatchID != "core" {
		t.Errorf("webhook got %+v, want only core/any", got)
	}
}

func TestDeliverIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", alertWatch("core", alert("any", channel.File)))
	f.save(t, article("a1", "pub", "Acme reports annual results", day(9, 0), day(10, 0)))
	f.evaluate(t, "alice")

	if d := f.deliver(t, "alice"); d != (Delivery{Sent: 1}) {
		t.Fatalf("first Deliver = %+v", d)
	}
	if d := f.deliver(t, "alice"); d != (Delivery{}) {
		t.Errorf("second Deliver = %+v, want nothing", d)
	}
	f.evaluate(t, "alice")
	if d := f.deliver(t, "alice"); d != (Delivery{}) {
		t.Errorf("Deliver after another Evaluate = %+v, want nothing", d)
	}
	if got := len(f.file.notifications()); got != 1 {
		t.Errorf("file channel got %d notifications, want 1", got)
	}

	// A new process carries on from the database.
	again := New(f.l.cfg)
	if d, err := again.Deliver(context.Background(), "alice"); err != nil || d != (Delivery{}) {
		t.Errorf("Deliver by a new Ledger = %+v, %v; want nothing", d, err)
	}
	if got := len(f.file.notifications()); got != 1 {
		t.Errorf("file channel got %d notifications, want 1", got)
	}
}

func TestDeliverQuietHoursDeferThenSend(t *testing.T) {
	f := newFixture(t)
	quiet := alert("night", channel.File)
	quiet.Quiet = "22:00-07:00" // Berlin time
	f.saveWatch(t, "alice", alertWatch("core", quiet))
	f.save(t, article("a1", "pub", "Acme reports annual results", day(20, 0), day(21, 30)))

	f.at(day(22, 30)) // 23:30 in Berlin
	f.evaluate(t, "alice")
	if d := f.deliver(t, "alice"); d != (Delivery{Deferred: 1}) {
		t.Fatalf("Deliver in quiet hours = %+v, want one deferred", d)
	}
	if got := f.file.notifications(); len(got) != 0 {
		t.Fatalf("sent during quiet hours: %+v", got)
	}

	f.save(t, article("a2", "pub", "Acme raises its dividend", day(23, 0), day(23, 15)))
	f.at(day(23, 30)) // 00:30 in Berlin, still quiet
	f.evaluate(t, "alice")
	if d := f.deliver(t, "alice"); d != (Delivery{Deferred: 1}) {
		t.Fatalf("second Deliver in quiet hours = %+v", d)
	}

	f.at(time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)) // 07:00 in Berlin: the window has ended
	if d := f.deliver(t, "alice"); d != (Delivery{Sent: 1}) {
		t.Fatalf("Deliver after quiet hours = %+v, want one sent", d)
	}
	got := f.file.notifications()
	if len(got) != 1 || !reflect.DeepEqual(titles(got[0].Articles), []string{"Acme reports annual results", "Acme raises its dividend"}) {
		t.Fatalf("the deferred articles were not all sent: %+v", got)
	}
	if !got[0].At.Equal(day(23, 30)) {
		t.Errorf("At = %v, want the time of the newest hit", got[0].At)
	}
}

func TestDeliverCooldown(t *testing.T) {
	f := newFixture(t)
	cool := alert("any", channel.File)
	cool.Cooldown = "2h"
	f.saveWatch(t, "alice", alertWatch("core", cool))

	step := func(at time.Time, a *model.Article) Delivery {
		t.Helper()
		f.at(at)
		if a != nil {
			f.save(t, *a)
		}
		f.evaluate(t, "alice")
		return f.deliver(t, "alice")
	}
	a1 := article("a1", "pub", "First story about Acme", day(8, 0), day(10, 0))
	a2 := article("a2", "pub", "Second story about Acme", day(8, 0), day(10, 30))
	a3 := article("a3", "pub", "Third story about Acme", day(8, 0), day(11, 0))
	a4 := article("a4", "pub", "Fourth story about Acme", day(8, 0), day(12, 5))

	if d := step(day(10, 0), &a1); d != (Delivery{Sent: 1}) {
		t.Fatalf("10:00 = %+v, want sent", d)
	}
	if d := step(day(10, 30), &a2); d != (Delivery{Deferred: 1}) {
		t.Fatalf("10:30 = %+v, want deferred by the cooldown", d)
	}
	if d := step(day(11, 0), &a3); d != (Delivery{Deferred: 1}) {
		t.Fatalf("11:00 = %+v, want deferred", d)
	}
	if d := step(day(12, 0), nil); d != (Delivery{Sent: 1}) {
		t.Fatalf("12:00 = %+v, want sent once the cooldown has passed", d)
	}
	if d := step(day(12, 5), &a4); d != (Delivery{Deferred: 1}) {
		t.Fatalf("12:05 = %+v, want deferred again", d)
	}

	got := f.file.notifications()
	if len(got) != 2 {
		t.Fatalf("file channel got %d notifications, want 2", len(got))
	}
	if !reflect.DeepEqual(titles(got[1].Articles), []string{"Second story about Acme", "Third story about Acme"}) || got[1].Title != "Watch core: any (2)" {
		t.Errorf("the second notification = %q %v, want the two deferred stories", got[1].Title, titles(got[1].Articles))
	}
}

func TestDeliverRetriesAfterSendFailure(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", alertWatch("core", alert("any", channel.File)))
	f.save(t, article("a1", "pub", "Acme reports annual results", day(9, 0), day(10, 0)))
	f.at(day(10, 0))
	f.evaluate(t, "alice")
	f.file.fail = 1

	d, err := f.l.Deliver(context.Background(), "alice")
	if err == nil || !strings.Contains(err.Error(), "send to file") || !strings.Contains(err.Error(), `alert "any"`) {
		t.Errorf("error = %v, want one that names the channel and the alert", err)
	}
	if d != (Delivery{Failed: 1}) {
		t.Fatalf("Deliver = %+v, want one failed", d)
	}

	// A newer hit arrives before the retry; the retry holds both and takes the
	// time of the newest hit, so a file channel names one stable file per batch.
	f.save(t, article("a2", "pub", "Acme raises its dividend", day(10, 20), day(10, 25)))
	f.at(day(10, 30))
	f.evaluate(t, "alice")
	if d := f.deliver(t, "alice"); d != (Delivery{Sent: 1}) {
		t.Fatalf("retry = %+v, want sent", d)
	}
	got := f.file.notifications()
	if len(got) != 1 || len(got[0].Articles) != 2 || !got[0].At.Equal(day(10, 30)) {
		t.Fatalf("sent %+v, want one notification with both articles at 10:30", got)
	}
	if d := f.deliver(t, "alice"); d != (Delivery{}) {
		t.Errorf("Deliver after success = %+v, want nothing", d)
	}

	// The same batch retried gets the same time.
	f.save(t, article("a3", "pub", "Acme appoints a director", day(11, 0), day(11, 0)))
	f.at(day(11, 5))
	f.evaluate(t, "alice")
	f.file.fail = 1
	f.l.Deliver(context.Background(), "alice")
	f.at(day(11, 50))
	f.deliver(t, "alice")
	got = f.file.notifications()
	if len(got) != 2 || !got[1].At.Equal(day(11, 5)) {
		t.Errorf("retried notification At = %v, want the hit time 11:05 (not the retry time)", got[len(got)-1].At)
	}
}

func TestDeliverWithTwoChannelsMarksOnlyAfterBothSucceed(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", alertWatch("core", alert("any", channel.File, channel.Webhook)))
	f.save(t, article("a1", "pub", "Acme reports annual results", day(9, 0), day(10, 0)))
	f.evaluate(t, "alice")
	f.webhook.fail = 1

	if d, err := f.l.Deliver(context.Background(), "alice"); err == nil || d != (Delivery{Failed: 1}) {
		t.Fatalf("Deliver = %+v, %v; want one failed and an error", d, err)
	}
	if d := f.deliver(t, "alice"); d != (Delivery{Sent: 1}) {
		t.Fatalf("retry = %+v, want sent", d)
	}
	if got := len(f.webhook.notifications()); got != 1 {
		t.Errorf("webhook got %d notifications, want 1", got)
	}
	// The file channel got the batch twice with the same time, which overwrites
	// one file there.
	got := f.file.notifications()
	if len(got) != 2 || !got[0].At.Equal(got[1].At) || got[0].Key() != got[1].Key() {
		t.Errorf("file notifications = %+v, want two attempts with one name", got)
	}
}

func TestDeliverTUIAndUnconfiguredKinds(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", alertWatch("core",
		alert("tui-only", channel.TUI),
		alert("mail-only", channel.Email),
		alert("feed-only", channel.Feed),
		alert("mixed", channel.TUI, channel.Email, channel.File),
	))
	f.saveWatch(t, "alice", alertWatch("second", alert("mail-only", channel.Email)))
	f.save(t, article("a1", "pub", "Acme reports annual results", day(9, 0), day(10, 0)))
	f.evaluate(t, "alice")

	d := f.deliver(t, "alice")
	if d != (Delivery{Sent: 1}) {
		t.Errorf("Deliver = %+v, want only the mixed alert sent", d)
	}
	if got := f.file.notifications(); len(got) != 1 || got[0].Alert != "mixed" {
		t.Errorf("file channel got %+v, want the mixed alert", got)
	}
	pending, err := f.db.Tenant("alice").PendingAlerts(context.Background())
	if err != nil || len(pending) != 0 {
		t.Errorf("pending = %+v, %v; want none (tui and unconfigured kinds count as delivered)", pending, err)
	}
	// One warning per kind and call, however many alerts name it.
	if n := strings.Count(f.logs.String(), "no channel configured"); n != 2 {
		t.Errorf("%d warnings, want 2 (email once, feed once):\n%s", n, f.logs.String())
	}
	if !strings.Contains(f.logs.String(), "channel=email") || !strings.Contains(f.logs.String(), "channel=feed") {
		t.Errorf("warnings do not name the kinds:\n%s", f.logs.String())
	}

	// The skipped alerts do not come back.
	f.logs.Reset()
	f.deliver(t, "alice")
	if strings.Contains(f.logs.String(), "no channel configured") {
		t.Errorf("a second call warned again:\n%s", f.logs.String())
	}
}

func TestDeliverIgnoresPolicyWhenNothingIsSent(t *testing.T) {
	f := newFixture(t)
	a := alert("mail", channel.Email)
	a.Quiet = "00:00-23:59"
	f.saveWatch(t, "alice", alertWatch("core", a))
	f.save(t, article("a1", "pub", "Acme reports annual results", day(9, 0), day(10, 0)))
	f.evaluate(t, "alice")
	if d := f.deliver(t, "alice"); d != (Delivery{}) {
		t.Errorf("Deliver = %+v, want nothing sent or deferred", d)
	}
	if p, _ := f.db.Tenant("alice").PendingAlerts(context.Background()); len(p) != 0 {
		t.Errorf("pending = %+v, want none", p)
	}
}

func TestDeliverNeverSendsInvisibleArticles(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "bob", alertWatch("core", alert("any", channel.File)))
	f.save(t,
		article("p1", "pub", "Public annual results", day(9, 0), day(10, 0)),
		article("i1", "int", "Internal annual results", day(9, 0), day(10, 0)),
	)
	// A hit for an article bob may not see, as left by an older licence or a
	// bug elsewhere: it is dropped at delivery.
	err := f.db.Tenant("bob").PutHits(context.Background(), []watch.Hit{
		{WatchID: "core", ArticleID: "i1", At: day(10, 0), Alerts: []string{"any"}},
		{WatchID: "core", ArticleID: "gone", At: day(10, 0), Alerts: []string{"any"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.evaluate(t, "bob")

	if d := f.deliver(t, "bob"); d != (Delivery{Sent: 1}) {
		t.Fatalf("Deliver = %+v", d)
	}
	got := f.file.notifications()
	if len(got) != 1 || len(got[0].Articles) != 1 || got[0].Articles[0].ID != "p1" {
		t.Fatalf("sent %+v, want only p1", got)
	}
	if strings.Contains(got[0].Body, "Internal") || strings.Contains(got[0].Title, "Internal") {
		t.Errorf("the notification mentions an invisible article: %q\n%s", got[0].Title, got[0].Body)
	}
	if p, _ := f.db.Tenant("bob").PendingAlerts(context.Background()); len(p) != 0 {
		t.Errorf("pending = %+v, want none: the invisible and missing articles are recorded as handled", p)
	}
}

func TestDeliverLeavesAlertsOfUnknownWatchesPending(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", alertWatch("core", alert("any", channel.File)))
	f.save(t, article("a1", "pub", "Acme reports annual results", day(9, 0), day(10, 0)))
	f.evaluate(t, "alice")
	f.saveWatch(t, "alice", alertWatch("core", alert("renamed", channel.File)))
	if d := f.deliver(t, "alice"); d != (Delivery{}) {
		t.Errorf("Deliver = %+v, want nothing for a removed alert", d)
	}
	if p, _ := f.db.Tenant("alice").PendingAlerts(context.Background()); len(p) != 1 {
		t.Errorf("pending = %+v, want the one alert kept", p)
	}
}

func TestDeliverReportsStoreFailure(t *testing.T) {
	f := newFixture(t)
	f.saveWatch(t, "alice", alertWatch("core", alert("any", channel.File)))
	if _, err := f.l.Deliver(context.Background(), "alice"); err != nil {
		t.Fatalf("Deliver with nothing pending: %v", err)
	}
	f.db.Close()
	if _, err := f.l.Deliver(context.Background(), "alice"); err == nil {
		t.Error("Deliver on a closed store returned no error")
	}
}
