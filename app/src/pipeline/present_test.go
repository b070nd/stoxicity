package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
)

// presented scores an article with the real lens and presents it by its claims.
func presented(t *testing.T, title, summary string, tickers ...string) (model.Score, model.Score) {
	t.Helper()
	l := lens.New(config.Lens{Threshold: 0.5}, lens.Options{})
	a := model.Article{ID: "a1", Title: title, Summary: summary, Tickers: tickers, Published: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)}
	a.Score = l.Score(a)
	names := func(tk string) []string {
		return map[string][]string{"ACME": {"Acme Corp"}, "BOLT": {"Bolt Industries"}}[tk]
	}
	return a.Score, Present(a, Claims(a, names, nil))
}

func TestPresentationFollowsTheClaimsAndNotTheKeywordOrder(t *testing.T) {
	cases := []struct {
		name, title, summary string
		kind                 model.EventKind
		why                  string
	}{
		{"a ruled-out deal is no event", "Acme Corp rules out an acquisition of Bolt Industries", "", model.EventOther, "negated"},
		{"a denied bankruptcy is no event", "Acme Corp denies bankruptcy rumours", "", model.EventOther, "denied"},
		{"a ruled-out bankruptcy is no event", "Acme Corp rules out bankruptcy", "", model.EventOther, "negated"},
		{"a bankruptcy that is called false", "Reports that Acme Corp files for bankruptcy protection are false, the company said", "", model.EventOther, "denied"},
		{"a background buyback is history", "Acme Corp reports steady sales", "The company has repurchased shares for years under a long-running programme.", model.EventOther, "about the past"},
		{"a suspended dividend is not a routine dividend", "Acme Corp suspends its dividend", "", model.EventDividend, "action suspended"},
		{"earnings lead a headline that ends with a buyback", "Acme Corp posts record earnings and a new share buyback", "", model.EventResults, "also current: buyback"},
		{"the deal outranks a dividend whatever the word order", "Acme Corp agrees to acquire Bolt Industries and raises dividend", "", model.EventAcquisition, "also current: dividend"},
		{"the same, the other way round", "Acme Corp raises dividend and agrees to acquire Bolt Industries", "", model.EventAcquisition, "also current: dividend"},
	}
	for _, c := range cases {
		lensScore, got := presented(t, c.title, c.summary, "ACME", "BOLT")
		if got.Kind != c.kind {
			t.Errorf("%s: kind %s (lens first keyword: %s), want %s; %v", c.name, got.Kind, lensScore.Kind, c.kind, got.Reasons)
		}
		if !strings.Contains(strings.Join(got.Reasons, " | "), c.why) {
			t.Errorf("%s: the reasons must say %q: %v", c.name, c.why, got.Reasons)
		}
	}
}

func TestASuspendedDividendIsMoreMaterialAndAnEventlessArticleLosesRelevance(t *testing.T) {
	_, susp := presented(t, "Acme Corp suspends its dividend", "", "ACME")
	_, plain := presented(t, "Acme Corp declares a dividend", "", "ACME")
	if susp.Materiality <= plain.Materiality {
		t.Errorf("suspending is not routine declaring: %d vs %d", susp.Materiality, plain.Materiality)
	}
	lensScore, denied := presented(t, "Acme Corp rules out an acquisition of Bolt Industries", "", "ACME", "BOLT")
	if lensScore.Kind != model.EventAcquisition || denied.Relevance >= lensScore.Relevance || denied.Materiality != 1 {
		t.Errorf("a ruled-out deal is not a deal: relevance %.2f -> %.2f, materiality %d -> %d", lensScore.Relevance, denied.Relevance, lensScore.Materiality, denied.Materiality)
	}
}

func TestPresentationKeepsTheLensReasonsAndVersionsAChange(t *testing.T) {
	lensScore, got := presented(t, "Acme Corp rules out an acquisition of Bolt Industries", "", "ACME", "BOLT")
	if len(got.Reasons) <= len(lensScore.Reasons) || got.Reasons[0] != lensScore.Reasons[0] {
		t.Errorf("the lens's reasons stay, the claims' follow: %v", got.Reasons)
	}
	if !strings.HasSuffix(got.LensVersion, "+assert-2") {
		t.Errorf("a changed reading carries the claims' version: %s", got.LensVersion)
	}
	// A macro article names no company and keeps the lens's reading.
	l2, same := presented(t, "Federal Reserve leaves interest rates unchanged", "", "")
	if same.Kind != l2.Kind || same.LensVersion != l2.LensVersion {
		t.Errorf("a rate decision is not 'no event': %+v vs %+v", same, l2)
	}
}
