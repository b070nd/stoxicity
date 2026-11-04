package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/watch"
)

var now = time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func art(id, source, domain, title string, ago time.Duration, rel float64, tickers ...string) model.Article {
	return model.Article{
		ID: id, SourceID: source, Domain: domain, URL: "https://" + domain + "/" + id, Title: title,
		Published: now.Add(-ago), Fetched: now.Add(-ago), Tickers: tickers,
		Score: model.Score{Relevance: rel, Materiality: 3, LensVersion: "v1"},
	}
}

// setup writes a configuration folder and fills its database:
// a public source, and an internal one owned by another tenant.
func setup(t *testing.T) (dir string) {
	t.Helper()
	for _, name := range []string{"STOXICITY_TIMEZONE", "STOXICITY_DATA_DIR", "STOXICITY_TENANT", "STOXICITY_LOG_LEVEL"} {
		t.Setenv(name, "")
	}
	dir = t.TempDir()
	write(t, dir, "config.yaml", "since: 2026-10-01\ndata_dir: ./data\nwatchlist: [KO]\n")
	write(t, dir, "sources.yaml", `sources:
  - id: pub
    name: Public
    domain: pub.example.com
    kind: rss
    url: https://pub.example.com/feed.xml
    tier: 2
    licence: redistributable
    terms: https://pub.example.com/terms
    enabled: true
  - id: secret
    name: Secret
    domain: secret.example.com
    kind: rss
    url: https://secret.example.com/feed.xml
    tier: 2
    licence: internal
    owner: other
    enabled: true
`)
	write(t, dir, "tenants/default/tenant.yaml", "id: default\nname: Default\ndefault_watch: nvda\n")
	write(t, dir, "tenants/default/watches/nvda.yaml", "id: nvda\nname: Nvidia\nscope:\n  tickers: [NVDA]\n")
	write(t, dir, "tenants/default/watches/dividends.yaml", "id: dividends\nname: Dividends\nquery: dividend\n")

	db, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Save(context.Background(), []model.Article{
		art("a1", "pub", "pub.example.com", "Nvidia beats estimates", 2*time.Hour, 0.9, "NVDA"),
		art("a2", "pub", "pub.example.com", "Coca-Cola raises dividend", 30*time.Hour, 0.8, "KO"),
		art("a3", "pub", "pub.example.com", "Meme chatter", 3*time.Hour, 0.1),
		art("a4", "secret", "secret.example.com", "Secret Nvidia dividend memo", time.Hour, 0.9, "NVDA"),
	})
	if err != nil {
		t.Fatal(err)
	}
	hit := watch.Hit{WatchID: "nvda", ArticleID: "a1", At: now.Add(-time.Hour)}
	if err := db.Tenant("default").PutHits(context.Background(), []watch.Hit{hit}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func open(t *testing.T, dir string) *Workspace {
	t.Helper()
	w, err := Open(Options{Root: dir, Cwd: dir, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func ids(arts []model.Article) []string {
	out := make([]string, len(arts))
	for i, a := range arts {
		out[i] = a.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOpenReadsTheTenantAndItsDefaults(t *testing.T) {
	w := open(t, setup(t))
	if w.Tenant() != "default" || w.DefaultWatch() != "nvda" {
		t.Errorf("tenant %q, default watch %q", w.Tenant(), w.DefaultWatch())
	}
	if got := w.Watchlist(); len(got) != 1 || got[0] != "KO" {
		t.Errorf("watchlist = %v", got)
	}
	if w.Threshold() <= 0 || w.Threshold() >= 1 {
		t.Errorf("threshold = %v", w.Threshold())
	}
	if !w.Now().Equal(now) {
		t.Errorf("now = %v", w.Now())
	}
}

func TestOpenUnknownTenantFails(t *testing.T) {
	dir := setup(t)
	if _, err := Open(Options{Root: dir, Cwd: dir, Tenant: "nobody"}); err == nil {
		t.Error("an unknown tenant should fail")
	}
}

func TestOpenWithoutConfigFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(Options{Root: dir, Cwd: dir}); err == nil {
		t.Error("an empty folder is not a configuration")
	}
}

func TestOpenCommandLineWatchBeatsTenantDefault(t *testing.T) {
	dir := setup(t)
	w, err := Open(Options{Root: dir, Cwd: dir, Watch: "dividends"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.DefaultWatch() != "dividends" {
		t.Errorf("default watch = %q", w.DefaultWatch())
	}
}

func TestArticlesAreLimitedToWhatTheTenantMaySee(t *testing.T) {
	w := open(t, setup(t))
	got, err := w.Articles(context.Background(), "", now.AddDate(0, 0, -7), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a1", "a3", "a2"}; !equal(ids(got), want) {
		t.Errorf("articles = %v, want %v (a4 belongs to another tenant's internal source)", ids(got), want)
	}
}

func TestArticlesOfAWatchFollowItsScopeAndQuery(t *testing.T) {
	w := open(t, setup(t))
	ctx := context.Background()
	from, to := now.AddDate(0, 0, -7), now.Add(time.Hour)

	tickers, err := w.Articles(ctx, "nvda", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(ids(tickers), []string{"a1"}) {
		t.Errorf("nvda = %v", ids(tickers))
	}
	query, err := w.Articles(ctx, "dividends", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(ids(query), []string{"a2"}) {
		t.Errorf("dividends = %v", ids(query))
	}
	if _, err := w.Articles(ctx, "nope", from, to); err == nil {
		t.Error("an unknown watch should fail")
	}
}

func TestSearchAppliesTheWatch(t *testing.T) {
	w := open(t, setup(t))
	ctx := context.Background()
	all, err := w.Search(ctx, "", "nvidia", time.Time{}, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(ids(all), []string{"a1"}) {
		t.Errorf("search = %v (the secret memo must not appear)", ids(all))
	}
	none, err := w.Search(ctx, "dividends", "nvidia", time.Time{}, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("a1 is not a dividend article: %v", ids(none))
	}
	if _, err := w.Search(ctx, "", `"unclosed`, time.Time{}, time.Time{}, 10); err == nil {
		t.Error("a bad query should fail")
	}
}

func TestWatchesCarryUnreadCountsAndMarkSeenClearsThem(t *testing.T) {
	w := open(t, setup(t))
	ctx := context.Background()
	list, err := w.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unread := map[string]int{}
	for _, x := range list {
		unread[x.ID] = x.Unread
	}
	if len(list) != 2 || unread["nvda"] != 1 || unread["dividends"] != 0 {
		t.Fatalf("watches = %+v", list)
	}
	if err := w.MarkSeen(ctx, "nvda"); err != nil {
		t.Fatal(err)
	}
	list, _ = w.Watches(ctx)
	for _, x := range list {
		if x.Unread != 0 {
			t.Errorf("%s still has %d unread", x.ID, x.Unread)
		}
	}
	if err := w.MarkSeen(ctx, ""); err == nil {
		t.Error("all news has no read marker")
	}
}

func TestLabelsRoundTripAndRefuseInvisibleArticles(t *testing.T) {
	w := open(t, setup(t))
	ctx := context.Background()
	if err := w.Label(ctx, "a1", true); err != nil {
		t.Fatal(err)
	}
	if err := w.Label(ctx, "a3", false); err != nil {
		t.Fatal(err)
	}
	v, err := w.Verdicts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v["a1"] != labels.Keep || v["a3"] != labels.Drop || len(v) != 2 {
		t.Errorf("verdicts = %v", v)
	}
	if err := w.Label(ctx, "a4", true); err == nil {
		t.Error("an article of an invisible source must not be labelled")
	}
	if err := w.Label(ctx, "missing", true); err == nil {
		t.Error("an unknown article must not be labelled")
	}
}

func TestSourcesListsEveryConfiguredSource(t *testing.T) {
	w := open(t, setup(t))
	rows, err := w.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != "pub" || rows[1].ID != "secret" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Health != "" || !rows[0].Enabled || rows[0].Licence != "redistributable" {
		t.Errorf("a source that was never polled has no health yet: %+v", rows[0])
	}
}

func TestCompanyNameIsEmptyWithoutAMaster(t *testing.T) {
	w := open(t, setup(t))
	if got := w.CompanyName("NVDA"); got != "" {
		t.Errorf("name = %q", got)
	}
}
