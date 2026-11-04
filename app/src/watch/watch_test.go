package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/model"
)

func art(domain string, tickers, topics []string, s model.Score) model.Article {
	return model.Article{ID: domain + strings.Join(tickers, ""), Domain: domain, Tickers: tickers, Topics: topics, Score: s}
}

var opts = Options{DefaultThreshold: 0.5}

func TestMatchSubjectsAreAUnion(t *testing.T) {
	w := Watch{Scope: Scope{Tickers: []string{"KO"}, Topics: []string{"ai-infra"}}}
	good := model.Score{Relevance: 0.9}
	cases := []struct {
		name string
		a    model.Article
		want bool
	}{
		{"ticker only", art("sec.gov", []string{"ko"}, nil, good), true}, // case-insensitive
		{"topic only", art("sec.gov", nil, []string{"ai-infra"}, good), true},
		{"neither", art("sec.gov", []string{"PEP"}, []string{"banks"}, good), false},
	}
	for _, c := range cases {
		if got := w.Match(c.a, opts); got != c.want {
			t.Errorf("%s: Match = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMatchNoSubjectsMeansEverything(t *testing.T) {
	a := art("sec.gov", []string{"X"}, nil, model.Score{Relevance: 0.9})
	if !(Watch{}).Match(a, opts) {
		t.Error("an empty watch should match any relevant article")
	}
}

func TestMatchConstraints(t *testing.T) {
	base := model.Score{Relevance: 0.9, Materiality: 3, Kind: model.EventAcquisition}
	w := Watch{
		Scope: Scope{Domains: []string{"sec.gov"}, ExcludeDomains: []string{"spam.example"}, Kinds: []model.EventKind{model.EventAcquisition}},
		Lens:  Lens{MinMateriality: 3},
	}
	if !w.Match(art("sec.gov", nil, nil, base), opts) {
		t.Fatal("expected a match")
	}
	low := base
	low.Materiality = 2
	wrongKind := base
	wrongKind.Kind = model.EventPriceChatter
	weak := base
	weak.Relevance = 0.4
	for name, a := range map[string]model.Article{
		"materiality too low": art("sec.gov", nil, nil, low),
		"wrong kind":          art("sec.gov", nil, nil, wrongKind),
		"below threshold":     art("sec.gov", nil, nil, weak),
		"domain not listed":   art("other.com", nil, nil, base),
	} {
		if w.Match(a, opts) {
			t.Errorf("%s: matched, want no match", name)
		}
	}
	w.Scope.ExcludeDomains = []string{"SEC.gov"}
	if w.Match(art("sec.gov", nil, nil, base), opts) {
		t.Error("exclude_domains should win and be case-insensitive")
	}
}

func TestMatchWatchThresholdOverridesDefault(t *testing.T) {
	a := art("x.com", nil, nil, model.Score{Relevance: 0.6})
	if !(Watch{}).Match(a, opts) {
		t.Fatal("0.6 clears the default 0.5")
	}
	if (Watch{Lens: Lens{Threshold: 0.8}}).Match(a, opts) {
		t.Error("the watch's own threshold 0.8 should reject 0.6")
	}
}

func TestMatchQuery(t *testing.T) {
	a := art("x.com", nil, nil, model.Score{Relevance: 0.9})
	w := Watch{Query: "anything"}
	no := Options{DefaultThreshold: 0.5, Query: func(model.Article) bool { return false }}
	if w.Match(a, no) {
		t.Error("a failing query should reject the article")
	}
	if !(Watch{}).Match(a, no) {
		t.Error("a watch without a query ignores Options.Query")
	}
}

func TestEvaluateAlerts(t *testing.T) {
	w := Watch{ID: "w", Alerts: []Alert{
		{Name: "big", When: When{MinMateriality: 4}},
		{Name: "any", When: When{}},
	}}
	articles := []model.Article{
		{ID: "1", Score: model.Score{Relevance: 0.9, Materiality: 5}, Published: time.Unix(10, 0)},
		{ID: "2", Score: model.Score{Relevance: 0.9, Materiality: 2}},
		{ID: "3", Score: model.Score{Relevance: 0.1}}, // not relevant: no hit at all
	}
	hits := Evaluate(w, articles, opts)
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want 2", hits)
	}
	if got := strings.Join(hits[0].Alerts, ","); got != "big,any" {
		t.Errorf("article 1 alerts = %q, want big,any", got)
	}
	if got := strings.Join(hits[1].Alerts, ","); got != "any" {
		t.Errorf("article 2 alerts = %q, want any", got)
	}
}

func valid() Watch {
	return Watch{
		ID: "buffett-core", Name: "Buffett core",
		Alerts: []Alert{{Name: "material", When: When{MinMateriality: 4},
			Channels: []channel.Kind{channel.TUI, channel.Email}, Quiet: "22:00-07:00", Cooldown: "2h"}},
		Digest: Digest{Cadence: "daily", At: "07:30", Channels: []channel.Kind{channel.Email}},
	}
}

func TestValidateOK(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid watch rejected: %v", err)
	}
	if err := (Watch{ID: "a", Name: "A"}).Validate(); err != nil {
		t.Fatalf("minimal watch rejected: %v", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	w := valid()
	w.ID = "Bad ID"
	w.Lens.Threshold = 2
	w.Alerts[0].Channels = []channel.Kind{"pigeon"}
	w.Alerts[0].Quiet = "late"
	w.Alerts[0].Cooldown = "soon"
	w.Digest.At = "7am"
	w.Dashboard = nil
	err := w.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"id must", "threshold", "pigeon", "quiet hours", "cooldown", "digest.at"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func TestValidateDuplicateAndMissing(t *testing.T) {
	w := valid()
	w.Alerts = append(w.Alerts, Alert{Name: "material", Channels: []channel.Kind{channel.TUI}})
	w.Alerts = append(w.Alerts, Alert{Name: "silent"})
	err := w.Validate()
	if err == nil || !strings.Contains(err.Error(), "used twice") || !strings.Contains(err.Error(), "no channels") {
		t.Fatalf("want duplicate-name and no-channels errors, got %v", err)
	}
}

func TestAlertPolicy(t *testing.T) {
	p := valid().Alerts[0].Policy()
	if p.Cooldown != 2*time.Hour || p.Quiet.Empty() {
		t.Fatalf("policy = %+v", p)
	}
}

func TestDomainsMatchSubdomains(t *testing.T) {
	good := model.Score{Relevance: 0.9}
	w := Watch{Scope: Scope{Domains: []string{"sec.gov"}}}
	for domain, want := range map[string]bool{
		"sec.gov": true, "www.sec.gov": true, "data.SEC.gov": true, "sec.gov.": true,
		"notsec.gov": false, "sec.gov.evil.com": false, "gov": false,
	} {
		if got := w.Match(art(domain, nil, nil, good), opts); got != want {
			t.Errorf("domain %q: Match = %v, want %v", domain, got, want)
		}
	}
	ex := Watch{Scope: Scope{ExcludeDomains: []string{"example.com"}}}
	if ex.Match(art("news.example.com", nil, nil, good), opts) {
		t.Error("exclude_domains should cover subdomains too")
	}
}

func TestGroupsSelectFamilies(t *testing.T) {
	w := Watch{Scope: Scope{Groups: []model.EventGroup{model.GroupIntegrity}}}
	hit := model.Score{Relevance: 0.9, Kind: model.EventNonReliance}
	miss := model.Score{Relevance: 0.9, Kind: model.EventDividend}
	if !w.Match(art("a.com", nil, nil, hit), opts) {
		t.Error("a non-reliance filing belongs to the integrity group")
	}
	if w.Match(art("a.com", nil, nil, miss), opts) {
		t.Error("a dividend is not an integrity event")
	}
	if err := (Watch{ID: "a", Name: "A", Scope: Scope{Groups: []model.EventGroup{"nope"}}}).Validate(); err == nil {
		t.Error("an unknown group must be rejected")
	}
}

func TestEvaluateStampsTheDetectionTime(t *testing.T) {
	detected := time.Date(2026, 11, 3, 12, 30, 0, 0, time.UTC)
	articles := []model.Article{{
		ID: "late", Score: model.Score{Relevance: 0.9},
		Published: detected.Add(-150 * time.Minute), Fetched: detected,
	}}
	o := Options{DefaultThreshold: 0.5, Detected: detected}
	hits := Evaluate(Watch{ID: "w"}, articles, o)
	if len(hits) != 1 || !hits[0].At.Equal(detected) {
		t.Fatalf("hits = %+v, want one stamped %v (not the publication time)", hits, detected)
	}
	if hits := Evaluate(Watch{ID: "w"}, articles, opts); len(hits) != 1 || !hits[0].At.IsZero() {
		t.Errorf("without Detected the hit time = %+v, want zero", hits)
	}
}
