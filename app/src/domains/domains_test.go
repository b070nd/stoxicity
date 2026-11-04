// Synthetic fixture, not verified against live data.

package domains

import (
	"reflect"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

func art(domain string, relevance float64) model.Article {
	return model.Article{Domain: domain, Score: model.Score{Relevance: relevance}}
}

func TestAggregateNormalisesDomain(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Example.COM", "example.com"},
		{"example.com.", "example.com"},
		{"www.example.com", "example.com"},
		{"WWW.Example.com.", "example.com"},
		{"news.example.com", "news.example.com"}, // subdomains stay separate
		{"", "(unknown)"},
		{"  ", "(unknown)"},
		{"www.", "(unknown)"},
	}
	for _, c := range cases {
		got := Aggregate([]model.Article{art(c.in, 0.9)}, 0.5)
		if len(got) != 1 || got[0].Domain != c.want {
			t.Errorf("Aggregate(%q) = %+v, want domain %q", c.in, got, c.want)
		}
	}
}

func TestAggregateMergesNormalisedDomains(t *testing.T) {
	got := Aggregate([]model.Article{
		art("example.com", 0.9),
		art("WWW.example.com.", 0.1),
	}, 0.5)
	if len(got) != 1 || got[0].Total != 2 || got[0].Relevant != 1 {
		t.Fatalf("Aggregate = %+v, want one domain with total 2 and relevant 1", got)
	}
}

func TestAggregateOrder(t *testing.T) {
	got := Aggregate([]model.Article{
		art("c.com", 0.1),
		art("a.com", 0.9),
		art("a.com", 0.1),
		art("b.com", 0.9),
		art("b.com", 0.8),
		art("e.com", 0.9),
		art("d.com", 0.9),
	}, 0.5)
	// Relevant first; among equal relevant, more total first; then name.
	want := []string{"b.com", "a.com", "d.com", "e.com", "c.com"}
	var names []string
	for _, s := range got {
		names = append(names, s.Domain)
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestAggregateThresholdIsInclusive(t *testing.T) {
	got := Aggregate([]model.Article{art("a.com", 0.5), art("a.com", 0.49)}, 0.5)
	if len(got) != 1 || got[0].Relevant != 1 || got[0].Total != 2 {
		t.Fatalf("Aggregate = %+v, want relevant 1 of total 2", got)
	}
}

func TestAggregateEmpty(t *testing.T) {
	if got := Aggregate(nil, 0.5); len(got) != 0 {
		t.Fatalf("Aggregate(nil) = %+v, want none", got)
	}
}

func TestRatio(t *testing.T) {
	cases := []struct {
		s    Stat
		want float64
	}{
		{Stat{Total: 0}, 0},
		{Stat{Total: 4, Relevant: 1}, 0.25},
		{Stat{Total: 2, Relevant: 2}, 1},
	}
	for _, c := range cases {
		if got := c.s.Ratio(); got != c.want {
			t.Errorf("Ratio(%+v) = %v, want %v", c.s, got, c.want)
		}
	}
}
