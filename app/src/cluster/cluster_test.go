// Synthetic fixture, not verified against live data: every article here is invented for testing.

package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

var t0 = time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC)

func article(id, domain, rawURL, title string, published time.Time) model.Article {
	return model.Article{ID: id, SourceID: domain, Domain: domain, URL: rawURL, Title: title, Published: published}
}

// wantGroupID computes the group ID from the specification, to check Build against it.
func wantGroupID(originalID string) string {
	sum := sha256.Sum256([]byte(originalID))
	return "c-" + hex.EncodeToString(sum[:])[:12]
}

// describe lists each group as its ID followed by its member IDs, in order.
func describe(groups []Group) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		ids := make([]string, len(g.Members))
		for i, m := range g.Members {
			ids[i] = m.ID
		}
		out = append(out, g.ID+": "+strings.Join(ids, ","))
	}
	return out
}

// rotated returns a copy of s starting at index k.
func rotated(s []model.Article, k int) []model.Article {
	out := make([]model.Article, 0, len(s))
	out = append(out, s[k:]...)
	return append(out, s[:k]...)
}

// reversed returns a reversed copy of s.
func reversed(s []model.Article) []model.Article {
	out := make([]model.Article, len(s))
	for i, a := range s {
		out[len(s)-1-i] = a
	}
	return out
}

func TestCanonical(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain URL unchanged", "https://example.com/news/story", "https://example.com/news/story"},
		{"case, www, default port, fragment and utm removed",
			"HTTPS://WWW.Example.COM:443/News/Story/?utm_source=x&id=7#top", "https://example.com/News/Story?id=7"},
		{"http default port removed", "http://example.com:80/a", "http://example.com/a"},
		{"non-default port kept", "http://example.com:8080/a/", "http://example.com:8080/a"},
		{"port 80 kept on https", "https://example.com:80/a", "https://example.com:80/a"},
		{"root path kept", "https://example.com/?fbclid=abc", "https://example.com/"},
		{"every exact tracking name removed",
			"https://example.com/a?gclid=1&ref=2&ref_src=3&mc_cid=4&mc_eid=5&cmpid=6&cid=7&igshid=8&yclid=9&_hsenc=10&_hsmi=11&keep=yes",
			"https://example.com/a?keep=yes"},
		{"parameters sorted by name", "https://example.com/a?b=2&a=1&c=3", "https://example.com/a?a=1&b=2&c=3"},
		{"utm names removed in any case", "https://example.com/a?UTM_Campaign=x&q=go", "https://example.com/a?q=go"},
		{"names that only look like tracking kept", "https://example.com/a?reference=1&refx=2", "https://example.com/a?reference=1&refx=2"},
		{"fragment removed", "https://example.com/a#section", "https://example.com/a"},
		{"one trailing slash stripped", "https://example.com/a/b/", "https://example.com/a/b"},
		{"escaped path kept escaped", "https://example.com/a%20b/", "https://example.com/a%20b"},
		{"subdomain other than www kept", "https://news.example.com/a", "https://news.example.com/a"},
		{"IPv6 host keeps brackets", "https://[::1]:443/x", "https://[::1]/x"},
		{"empty query marker dropped", "https://example.com/a?", "https://example.com/a"},
		{"empty input", "", ""},
		{"bad escape returned unchanged", "https://example.com/%zz", "https://example.com/%zz"},
		{"missing scheme returned unchanged", "://no-scheme", "://no-scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Canonical(tt.in); got != tt.want {
				t.Errorf("Canonical(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildMergesSyndicatedCopies(t *testing.T) {
	a := article("a1", "reuters.com", "https://www.reuters.com/markets/acme-beats?utm_source=feed", "Acme Corp beats earnings estimates", t0)
	b := article("b1", "example-news.com", "https://example-news.com/business/acme-beats/", "ACME Corp Beats Earnings Estimates!", t0.Add(2*time.Hour))
	c := article("c1", "blog.example.org", "http://blog.example.org/post?fbclid=xyz&id=9", "Acme corp beats earnings estimates", t0.Add(3*time.Hour))
	d := article("d1", "other.com", "https://other.com/x", "Gardening tips for spring", t0.Add(time.Hour))

	groups := Build([]model.Article{a, b, c, d})
	want := []string{wantGroupID("a1") + ": a1,b1,c1", wantGroupID("d1") + ": d1"}
	if got := describe(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	if groups[0].Original.ID != "a1" {
		t.Errorf("Original = %q, want the earliest publisher a1", groups[0].Original.ID)
	}
	for _, m := range groups[0].Members {
		if m.ClusterID != groups[0].ID {
			t.Errorf("member %s has ClusterID %q, want %q", m.ID, m.ClusterID, groups[0].ID)
		}
	}
}

func TestBuildMergesSameCanonicalURL(t *testing.T) {
	a := article("a1", "example.com", "https://example.com/story?utm_medium=email", "First headline here", t0)
	b := article("b1", "example.com", "https://EXAMPLE.com/story/#top", "Completely different wording", t0.Add(time.Hour))
	c := article("c1", "example.com", "https://example.com/other", "Unrelated story entirely", t0)

	groups := Build([]model.Article{a, b, c})
	want := []string{wantGroupID("a1") + ": a1,b1", wantGroupID("c1") + ": c1"}
	if got := describe(groups); !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

func TestBuildMergesSameID(t *testing.T) {
	a := article("acc-1", "sec.gov", "https://sec.gov/a", "Filing one", t0)
	b := article("acc-1", "mirror.example", "https://mirror.example/b", "Other words entirely", t0.Add(time.Hour))

	groups := Build([]model.Article{a, b})
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatalf("same ID should be one group: %v", describe(groups))
	}
}

func TestBuildDistinctStoriesDoNotMerge(t *testing.T) {
	apple := article("a1", "one.com", "https://one.com/apple", "Apple Reports Third Quarter Results", t0)
	msft := article("m1", "two.com", "https://two.com/msft", "Microsoft Reports Third Quarter Results", t0)

	groups := Build([]model.Article{apple, msft})
	if len(groups) != 2 {
		t.Fatalf("Apple and Microsoft quarter reports merged: %v", describe(groups))
	}
}

func TestBuildTransitive(t *testing.T) {
	// a and b merge on title. b and c merge on canonical URL. a and c share nothing.
	a := article("a1", "one.com", "https://one.com/a", "Bank raises quarterly dividend again", t0)
	b := article("b1", "two.com", "https://three.com/shared/", "Bank Raises Quarterly Dividend Again!", t0.Add(time.Hour))
	c := article("c1", "three.com", "https://three.com/shared?utm_source=x", "Something entirely unlike the others", t0.Add(2*time.Hour))

	groups := Build([]model.Article{c, a, b})
	want := []string{wantGroupID("a1") + ": a1,b1,c1"}
	if got := describe(groups); !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

func TestBuildPublishWindow(t *testing.T) {
	tests := []struct {
		name string
		gap  time.Duration
		want int
	}{
		{"47 hours apart merges", 47 * time.Hour, 1},
		{"exactly 48 hours apart merges", 48 * time.Hour, 1},
		{"48 hours and a second apart does not", 48*time.Hour + time.Second, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := article("a1", "one.com", "https://one.com/a", "Chipmaker unveils new processor line", t0)
			b := article("b1", "two.com", "https://two.com/b", "Chipmaker unveils new processor line", t0.Add(tt.gap))
			if got := len(Build([]model.Article{a, b})); got != tt.want {
				t.Errorf("got %d groups, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildZeroPublishedNeverMatchesOnTitle(t *testing.T) {
	tests := []struct {
		name string
		pubA time.Time
		pubB time.Time
	}{
		{"both zero", time.Time{}, time.Time{}},
		{"first zero", time.Time{}, t0},
		{"second zero", t0, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := article("a1", "one.com", "https://one.com/a", "Chipmaker unveils new processor line", tt.pubA)
			b := article("b1", "two.com", "https://two.com/b", "Chipmaker unveils new processor line", tt.pubB)
			if got := len(Build([]model.Article{a, b})); got != 2 {
				t.Errorf("got %d groups, want 2", got)
			}
		})
	}
}

func TestBuildOrdersGroupsWithZeroLast(t *testing.T) {
	undated := article("u1", "one.com", "https://one.com/u", "Undated item one", time.Time{})
	late := article("l1", "two.com", "https://two.com/l", "Later item two", t0.Add(5*time.Hour))
	early := article("e1", "three.com", "https://three.com/e", "Early item three", t0)

	groups := Build([]model.Article{undated, late, early})
	var order []string
	for _, g := range groups {
		order = append(order, g.Original.ID)
	}
	if want := []string{"e1", "l1", "u1"}; !reflect.DeepEqual(order, want) {
		t.Errorf("group order = %v, want %v", order, want)
	}
}

func TestBuildShuffleInvariant(t *testing.T) {
	batch := []model.Article{
		article("a1", "one.com", "https://one.com/a", "Bank raises quarterly dividend again", t0),
		article("b1", "two.com", "https://two.com/b?utm_source=q", "Bank Raises Quarterly Dividend Again!", t0.Add(time.Hour)),
		article("c1", "three.com", "https://three.com/c", "Apple Reports Third Quarter Results", t0),
		article("d1", "four.com", "https://four.com/d", "Microsoft Reports Third Quarter Results", t0),
		article("e1", "five.com", "https://five.com/e", "Gardening tips for a better spring season", t0.Add(3*time.Hour)),
		article("f1", "six.com", "https://six.com/f", "Gardening tips for a better spring season", t0.Add(3*time.Hour)),
		article("g1", "seven.com", "https://seven.com/g", "Unrelated story about rates", time.Time{}),
	}
	want := describe(Build(batch))
	if len(want) != 5 {
		t.Fatalf("baseline has %d groups, want 5: %v", len(want), want)
	}

	for k := range len(batch) {
		if got := describe(Build(rotated(batch, k))); !reflect.DeepEqual(got, want) {
			t.Errorf("rotation %d: got %v, want %v", k, got, want)
		}
	}
	if got := describe(Build(reversed(batch))); !reflect.DeepEqual(got, want) {
		t.Errorf("reversed: got %v, want %v", got, want)
	}
}

func TestAssign(t *testing.T) {
	in := []model.Article{
		article("a1", "one.com", "https://one.com/a", "Bank raises quarterly dividend again", t0),
		article("z9", "zz.com", "https://zz.com/z", "Gardening tips for a better spring season", t0),
		article("b1", "two.com", "https://two.com/b?utm_source=q", "Bank Raises Quarterly Dividend Again!", t0.Add(time.Hour)),
	}
	before := make([]model.Article, len(in))
	copy(before, in)

	out := Assign(in)
	if !reflect.DeepEqual(in, before) {
		t.Fatal("Assign modified its input")
	}
	if len(out) != len(in) {
		t.Fatalf("got %d articles, want %d", len(out), len(in))
	}
	for i := range out {
		if out[i].ID != in[i].ID {
			t.Errorf("position %d holds %q, want input order %q", i, out[i].ID, in[i].ID)
		}
		if out[i].ClusterID == "" {
			t.Errorf("article %s has no ClusterID", out[i].ID)
		}
	}
	if out[0].ClusterID != out[2].ClusterID {
		t.Errorf("duplicates have different clusters: %q and %q", out[0].ClusterID, out[2].ClusterID)
	}
	if out[0].ClusterID != wantGroupID("a1") {
		t.Errorf("cluster = %q, want %q", out[0].ClusterID, wantGroupID("a1"))
	}
	if out[1].ClusterID != wantGroupID("z9") {
		t.Errorf("singleton cluster = %q, want %q", out[1].ClusterID, wantGroupID("z9"))
	}
}

func TestBuildLargeBatch(t *testing.T) {
	const stories = 4000
	const copies = 1000

	var batch []model.Article
	// Each story has a unique token, so no two originals share enough words to match.
	for i := range stories {
		batch = append(batch, article(
			fmt.Sprintf("acc-%05d", i),
			fmt.Sprintf("origin%05d.example", i),
			fmt.Sprintf("https://origin%05d.example/news/%05d", i, i),
			fmt.Sprintf("Firm co%05d reports quarterly revenue growth", i),
			t0.Add(time.Duration(i)*time.Minute),
		))
	}
	// The first copies syndicate stories 0 to copies-1, with different case, punctuation and tracking.
	for k := range copies {
		orig := batch[k]
		batch = append(batch, article(
			fmt.Sprintf("copy-%05d", k),
			fmt.Sprintf("site%05d.example", k),
			fmt.Sprintf("https://www.site%05d.example/story/%05d/?utm_medium=feed&id=%05d", k, k, k),
			strings.ToUpper(orig.Title)+"!",
			orig.Published.Add(time.Hour),
		))
	}

	start := time.Now()
	groups := Build(batch)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Build took %v on %d articles, want under 2s", elapsed, len(batch))
	}
	if len(groups) != stories {
		t.Fatalf("got %d groups, want %d", len(groups), stories)
	}
	for _, g := range groups {
		k, err := strconv.Atoi(strings.TrimPrefix(g.Original.ID, "acc-"))
		if err != nil {
			t.Fatalf("unexpected original %q", g.Original.ID)
		}
		want := 1
		if k < copies {
			want = 2
		}
		if len(g.Members) != want {
			t.Errorf("story %d has %d members, want %d", k, len(g.Members), want)
		}
	}
}
