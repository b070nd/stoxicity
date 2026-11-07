package ledger

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/sources"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/watch"
)

var (
	pubSource = model.Source{ID: "pub", Domain: "pub.example", Licence: model.LicenceRedistributable}
	intSource = model.Source{ID: "int", Domain: "int.example", Licence: model.LicenceInternal, Owner: "alice"}
)

// clock is a settable clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// fakeChannel records what it is asked to send. While fail is positive a send
// fails and counts it down.
type fakeChannel struct {
	kind channel.Kind
	mu   sync.Mutex
	sent []channel.Notification
	fail int
}

func (f *fakeChannel) Kind() channel.Kind { return f.kind }

func (f *fakeChannel) Send(_ context.Context, n channel.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("connection refused")
	}
	f.sent = append(f.sent, n)
	return nil
}

func (f *fakeChannel) notifications() []channel.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]channel.Notification(nil), f.sent...)
}

// countingStore counts what ChangedAfter hands out.
type countingStore struct {
	store.Store
	calls, articles int
}

func (c *countingStore) ChangedAfter(ctx context.Context, after int64, allowed map[string]bool, limit int) ([]model.Article, int64, error) {
	out, last, err := c.Store.ChangedAfter(ctx, after, allowed, limit)
	c.calls++
	c.articles += len(out)
	return out, last, err
}

// fixture is a Ledger over a real store in a temporary folder.
type fixture struct {
	l       *Ledger
	db      *store.DB
	counter *countingStore
	clock   *clock
	file    *fakeChannel
	webhook *fakeChannel
	logs    *bytes.Buffer
	zone    *time.Location
	dir     string // holds one watches folder per tenant
}

// day is 3 November 2026, a Tuesday, in UTC.
func day(hour, minute int) time.Time {
	return time.Date(2026, 11, 3, hour, minute, 0, 0, time.UTC)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{
		db:      db,
		counter: &countingStore{Store: db},
		clock:   &clock{t: day(12, 0)},
		file:    &fakeChannel{kind: channel.File},
		webhook: &fakeChannel{kind: channel.Webhook},
		logs:    &bytes.Buffer{},
		zone:    zone,
		dir:     t.TempDir(),
	}
	f.l = New(Config{
		Store:   f.counter,
		Sources: sources.Registry{pubSource.ID: pubSource, intSource.ID: intSource},
		Watches: func(tenantID string) watch.Store { return watch.FSStore{Dir: f.dir + "/" + tenantID} },
		Channels: map[channel.Kind]channel.Channel{
			channel.File:    f.file,
			channel.Webhook: f.webhook,
		},
		Threshold: 0.5,
		Zone:      zone,
		Now:       f.clock.Now,
		Logger:    slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return f
}

// saveWatch writes a watch for a tenant.
func (f *fixture) saveWatch(t *testing.T, tenantID string, w watch.Watch) {
	t.Helper()
	if err := f.l.cfg.Watches(tenantID).Save(w); err != nil {
		t.Fatalf("save watch %q: %v", w.ID, err)
	}
}

// article is a relevant, scored article of the source, published and fetched at
// the given times.
func article(id, source, title string, published, fetched time.Time) model.Article {
	return model.Article{
		ID: id, SourceID: source, Domain: source + ".example", URL: "https://" + source + ".example/" + id,
		Title: title, Published: published, Fetched: fetched,
		Score: model.Score{Relevance: 0.9, Materiality: 3, Kind: model.EventBuyback, LensVersion: "l1"},
	}
}

// save stores articles in the base.
func (f *fixture) save(t *testing.T, as ...model.Article) {
	t.Helper()
	if err := f.db.Save(context.Background(), as); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// at moves the clock.
func (f *fixture) at(t time.Time) { f.clock.Set(t) }

// evaluate runs Evaluate for a tenant and fails the test on an error.
func (f *fixture) evaluate(t *testing.T, tenantID string) map[string]int {
	t.Helper()
	got, err := f.l.Evaluate(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return got
}

// deliver runs Deliver for a tenant and fails the test on an error.
func (f *fixture) deliver(t *testing.T, tenantID string) Delivery {
	t.Helper()
	d, err := f.l.Deliver(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	return d
}

// hitIDs lists the article IDs of a watch's hits, newest first.
func (f *fixture) hitIDs(t *testing.T, tenantID, watchID string) []string {
	t.Helper()
	hits, err := f.db.Tenant(tenantID).Hits(context.Background(), watchID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, h := range hits {
		out = append(out, h.ArticleID)
	}
	return out
}

func titles(as []model.Article) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Title)
	}
	return out
}

// alertWatch is a watch that follows everything and has one alert per name,
// each with the given channels.
func alertWatch(id string, alerts ...watch.Alert) watch.Watch {
	return watch.Watch{ID: id, Name: "Watch " + id, Alerts: alerts}
}

func alert(name string, kinds ...channel.Kind) watch.Alert {
	return watch.Alert{Name: name, Channels: kinds}
}
