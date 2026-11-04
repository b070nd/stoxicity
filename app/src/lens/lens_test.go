package lens

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/model"
)

// defaultCfg mirrors the boost and demote lists of app/config/config.yaml.
var defaultCfg = config.Lens{
	Threshold: 0.5,
	Boost: []string{
		"earnings", "annual report", "10-K", "guidance", "free cash flow",
		"competitive advantage", "moat", "buyback", "dividend", "insider buying",
		"spin-off", "capital allocation",
	},
	Demote: []string{
		"price target", "intraday", "options flow", "short squeeze",
		"meme stock", "technical analysis",
	},
}

var testItems = map[string]model.EventKind{
	"2.02": model.EventResults,
	"4.02": model.EventNonReliance,
	"5.02": model.EventOfficerChange,
}

func testOpts() Options {
	return Options{
		SourceTier: func(id string) model.Tier {
			switch id {
			case "edgar":
				return model.TierPrimary
			case "blog":
				return model.TierCommentary
			}
			return model.TierReporting
		},
		ItemKind: func(items []string) (model.EventKind, bool) {
			for _, it := range items {
				if k, ok := testItems[it]; ok {
					return k, true
				}
			}
			return "", false
		},
	}
}

func TestScoreGolden(t *testing.T) {
	l := New(defaultCfg, testOpts())
	tests := []struct {
		name string
		a    model.Article
		kind model.EventKind
		mat  int
		rel  float64
	}{
		{"8-K 2.02 results", model.Article{SourceID: "edgar", Form: "8-K", Items: []string{"2.02"},
			Title: "Acme Corp reports third quarter results"}, model.EventResults, 3, 0.49},
		{"8-K 2.02 results with earnings word", model.Article{SourceID: "edgar", Form: "8-K", Items: []string{"2.02"},
			Title: "Acme Corp 8-K", Summary: "Earnings release for the quarter ended June 30"}, model.EventResults, 3, 0.55},
		{"8-K 4.02 restatement", model.Article{SourceID: "edgar", Form: "8-K", Items: []string{"4.02"},
			Title: "Acme Corp: previously issued financial statements should no longer be relied upon"}, model.EventNonReliance, 4, 0.61},
		{"8-K 4.02 restatement with material weakness", model.Article{SourceID: "edgar", Form: "8-K", Items: []string{"4.02"},
			Title: "Acme Corp restatement", Summary: "The audit committee found a material weakness"}, model.EventNonReliance, 5, 0.73},
		{"analyst price target", model.Article{SourceID: "blog",
			Title: "Analyst raises price target on X"}, model.EventAnalystTgt, 1, 0.09},
		{"analyst upgrade", model.Article{SourceID: "wire",
			Title: "Broker upgrades X to buy, lifts price target"}, model.EventAnalystTgt, 1, 0.14},
		{"analyst note with boosts is capped", model.Article{SourceID: "edgar",
			Title: "Analyst upgrade cites earnings, guidance and moat"}, model.EventAnalystTgt, 1, 0.35},
		{"buyback", model.Article{SourceID: "wire",
			Title: "Company X announces $5 billion share repurchase program"}, model.EventBuyback, 3, 0.5},
		{"routine dividend", model.Article{SourceID: "wire",
			Title: "Company X declares regular quarterly dividend of $0.25 per share"}, model.EventDividend, 2, 0.38},
		{"commentary with demote words", model.Article{SourceID: "blog",
			Title: "Technical analysis: intraday swings and options flow on X stock"}, model.EventOther, 1, 0},
		{"empty text keeps the 8-K kind", model.Article{SourceID: "edgar", Form: "8-K", Items: []string{"2.02"},
			Title: "", Summary: "  \n"}, model.EventResults, 3, 0},
		{"form 4 purchase", model.Article{SourceID: "edgar", Form: "4",
			Title: "Form 4: Smith, CEO, purchase of 10,000 shares"}, model.EventInsiderBuy, 2, 0.37},
		{"form 4 sale", model.Article{SourceID: "edgar", Form: "4",
			Title: "Form 4 - director sale of shares"}, model.EventInsiderSell, 2, 0.37},
		{"form 4 without a word", model.Article{SourceID: "edgar", Form: "4",
			Title: "Statement of changes in beneficial ownership"}, model.EventOther, 1, 0.25},
		{"form 10-K", model.Article{SourceID: "edgar", Form: "10-K",
			Title: "Annual report for fiscal year 2025"}, model.EventResults, 3, 0.55},
		{"form 10-Q", model.Article{SourceID: "edgar", Form: "10-Q",
			Title: "Quarterly report"}, model.EventResults, 3, 0.49},
		{"cyber incident", model.Article{SourceID: "wire",
			Title: "Acme discloses cybersecurity incident affecting systems"}, model.EventCyber, 4, 0.56},
		{"bankruptcy with going concern", model.Article{SourceID: "wire",
			Title: "Acme files for Chapter 11; going concern doubt"}, model.EventBankruptcy, 5, 0.68},
		{"officer change", model.Article{SourceID: "wire",
			Title: "CEO to step down as board appoints interim chief executive"}, model.EventOfficerChange, 2, 0.32},
		{"guidance with magnitude", model.Article{SourceID: "wire",
			Title: "Acme raises outlook after record quarter"}, model.EventGuidance, 4, 0.56},
		{"price chatter", model.Article{SourceID: "wire",
			Title: "Acme shares plunge 12% in early trading"}, model.EventPriceChatter, 2, 0.32},
		{"acquisition", model.Article{SourceID: "wire",
			Title: "Acme to acquire Beta in all-stock merger"}, model.EventAcquisition, 3, 0.44},
		{"13D", model.Article{SourceID: "wire",
			Title: "Activist fund files 13D stake in Acme"}, model.Event13DG, 2, 0.32},
		{"rates", model.Article{SourceID: "wire",
			Title: "FOMC holds federal funds rate steady"}, model.EventRates, 2, 0.32},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := l.Score(tc.a)
			if got.Kind != tc.kind || got.Materiality != tc.mat || got.Relevance != tc.rel {
				t.Errorf("kind %s, materiality %d, relevance %v; want %s, %d, %v\nreasons: %q",
					got.Kind, got.Materiality, got.Relevance, tc.kind, tc.mat, tc.rel, got.Reasons)
			}
			if got.LensVersion != l.Version() {
				t.Errorf("LensVersion = %q, want %q", got.LensVersion, l.Version())
			}
			if got.Tone != 0 || got.Sector != "" {
				t.Errorf("tone %v and sector %q should stay zero", got.Tone, got.Sector)
			}
			if len(got.Reasons) == 0 {
				t.Error("no reasons")
			}
		})
	}
}

func TestEmptyTextReasons(t *testing.T) {
	got := New(defaultCfg, Options{}).Score(model.Article{})
	want := []string{"no kind rule matched", "kind other", "base materiality 1", "no text"}
	if got.Relevance != 0 || !reflect.DeepEqual(got.Reasons, want) {
		t.Errorf("got relevance %v, reasons %q; want 0, %q", got.Relevance, got.Reasons, want)
	}
}

func TestReasonsRecordEveryAdjustment(t *testing.T) {
	l := New(defaultCfg, testOpts())
	got := l.Score(model.Article{SourceID: "edgar", Form: "8-K", Items: []string{"2.02"},
		Title: "Acme record earnings in line with guidance"})
	want := []string{
		"8-K item 2.02",
		"kind results",
		"base materiality 3",
		`magnitude phrase "record": materiality +1`,
		`routine phrase "in line with": materiality -1`,
		`boost "earnings": relevance +0.06`,
		`boost "guidance": relevance +0.06`,
		"source tier primary: relevance +0.05",
	}
	if !reflect.DeepEqual(got.Reasons, want) {
		t.Errorf("reasons = %q\nwant      %q", got.Reasons, want)
	}
	if got.Materiality != 3 || got.Relevance != 0.61 {
		t.Errorf("materiality %d, relevance %v; want 3, 0.61", got.Materiality, got.Relevance)
	}
}

func TestMaterialityClamp(t *testing.T) {
	l := New(config.Lens{}, Options{})
	up := l.Score(model.Article{Title: "Acme files for bankruptcy after record losses"})
	if up.Materiality != 5 {
		t.Errorf("materiality = %d, want 5", up.Materiality)
	}
	noise := l.Score(model.Article{Title: "Analyst price target as expected"})
	if noise.Kind != model.EventAnalystTgt || noise.Materiality != 1 || len(noise.Reasons) != 3 {
		t.Errorf("noise kind %s materiality %d reasons %q; a routine tag must not apply", noise.Kind, noise.Materiality, noise.Reasons)
	}
	low := l.Score(model.Article{Title: "Acme store opening went as expected"})
	if low.Kind != model.EventOther || low.Materiality != 1 || !contains(low.Reasons, "materiality clamped to 1") {
		t.Errorf("kind %s materiality %d reasons %q; want other, 1 with a clamp reason", low.Kind, low.Materiality, low.Reasons)
	}
}

func TestRecordDateIsNotMagnitude(t *testing.T) {
	got := New(config.Lens{}, Options{}).Score(model.Article{Title: "Acme declares dividend payable to holders of record date May 1"})
	if got.Materiality != 3 {
		t.Errorf("materiality = %d, want 3", got.Materiality)
	}
}

func TestInferKind(t *testing.T) {
	l := New(config.Lens{}, Options{})
	tests := []struct {
		text string
		want model.EventKind
	}{
		{"Acme authorises new buyback", model.EventBuyback},
		{"Acme approves buy-back of shares", model.EventBuyback},
		{"Acme will repurchase stock", model.EventBuyback},
		{"Acme raises dividend", model.EventDividend},
		{"Acme files Chapter 11", model.EventBankruptcy},
		{"Acme heads toward bankruptcy", model.EventBankruptcy},
		{"Acme will restate prior results", model.EventNonReliance},
		{"Non-reliance on prior statements", model.EventNonReliance},
		{"Cyber attack hits Acme", model.EventCyber},
		{"Acme confirms breach of cloud systems, a cyber event", model.EventCyber},
		{"Acme faces delisting notice", model.EventDelisting},
		{"Acme plans spin-off of unit", model.EventSpinoff},
		{"Acme announces spinoff", model.EventSpinoff},
		{"Acme to acquire Beta", model.EventAcquisition},
		{"Acme and Beta agree merger", model.EventAcquisition},
		{"Takeover bid for Acme", model.EventAcquisition},
		{"Analyst changes price target", model.EventAnalystTgt},
		{"Broker downgrades Acme", model.EventAnalystTgt},
		{"Bank initiates coverage of Acme", model.EventAnalystTgt},
		{"Acme cuts guidance", model.EventGuidance},
		{"Acme full-year outlook", model.EventGuidance},
		{"Acme profit forecast", model.EventGuidance},
		{"Acme quarterly results", model.EventResults},
		{"Acme earnings beat", model.EventResults},
		{"Results for the year at Acme", model.EventResults},
		{"Acme fiscal year ends", model.EventResults},
		{"Acme CFO resigns", model.EventOfficerChange},
		{"Board appoints new chief executive", model.EventOfficerChange},
		{"Insider buying at Acme", model.EventInsiderBuy},
		{"Insider sale at Acme", model.EventInsiderSell},
		{"Schedule 13G filed for Acme", model.Event13DG},
		{"Acme loses lawsuit", model.EventLitigation},
		{"Acme agrees settlement", model.EventLitigation},
		{"Rate decision due Wednesday", model.EventRates},
		{"FOMC meets", model.EventRates},
		{"CPI cools", model.EventInflation},
		{"Inflation expectations fall", model.EventInflation},
		{"Acme stock jumps on news", model.EventPriceChatter},
		{"Shares of Acme fell sharply", model.EventPriceChatter},
		{"Acme opens new store", model.EventOther},
		{"Acme CEO speaks at conference", model.EventOther},
		{"Cyber conference opens", model.EventOther},
	}
	for _, tc := range tests {
		if got := l.Score(model.Article{Title: tc.text}).Kind; got != tc.want {
			t.Errorf("%q: kind %s, want %s", tc.text, got, tc.want)
		}
	}
	if len(kindRules) < 25 {
		t.Errorf("%d patterns, want at least 25", len(kindRules))
	}
}

func TestFirstRuleWins(t *testing.T) {
	l := New(config.Lens{}, Options{})
	got := l.Score(model.Article{Title: "Acme buyback and dividend, merger talks"}).Kind
	if got != model.EventBuyback {
		t.Errorf("kind = %s, want buyback", got)
	}
}

func TestFormOverridesTextButNotItems(t *testing.T) {
	l := New(config.Lens{}, testOpts())
	f4 := l.Score(model.Article{Form: "4", Title: "Form 4 purchase of dividend shares"})
	if f4.Kind != model.EventInsiderBuy {
		t.Errorf("form 4 kind = %s, want insider_buy", f4.Kind)
	}
	both := l.Score(model.Article{Form: "10-K", Items: []string{"5.02"}, Title: "Annual report"})
	if both.Kind != model.EventOfficerChange {
		t.Errorf("item kind = %s, want officer_change", both.Kind)
	}
	unknown := l.Score(model.Article{Form: "8-K", Items: []string{"9.99"}, Title: "Acme dividend"})
	if unknown.Kind != model.EventDividend {
		t.Errorf("unknown item falls back to text: kind = %s, want dividend", unknown.Kind)
	}
	sc := l.Score(model.Article{Form: "SC 13D/A", Title: "Amendment"})
	if sc.Kind != model.Event13DG {
		t.Errorf("SC 13D/A kind = %s, want 13d_13g", sc.Kind)
	}
}

func TestMonotonicInBaseMateriality(t *testing.T) {
	opts := Options{ItemKind: func(items []string) (model.EventKind, bool) {
		return model.EventKind(items[0]), true
	}}
	l := New(defaultCfg, opts)
	kinds := model.EventKinds()
	rel := make(map[model.EventKind]float64, len(kinds))
	for _, k := range kinds {
		s := l.Score(model.Article{Items: []string{string(k)}, Title: "Acme news"})
		if s.Kind != k {
			t.Fatalf("hook kind %s came back as %s", k, s.Kind)
		}
		rel[k] = s.Relevance
	}
	for _, hi := range kinds {
		for _, lo := range kinds {
			if hi.BaseMateriality() > lo.BaseMateriality() && rel[hi] < rel[lo] {
				t.Errorf("%s (base %d) scored %v, below %s (base %d) at %v",
					hi, hi.BaseMateriality(), rel[hi], lo, lo.BaseMateriality(), rel[lo])
			}
		}
	}
}

func TestKeywordCap(t *testing.T) {
	cfg := config.Lens{
		Boost:  []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"},
		Demote: []string{"one", "two", "three", "four", "five", "six", "seven"},
	}
	l := New(cfg, Options{})
	rel := func(title string) float64 { return l.Score(model.Article{Title: title}).Relevance }
	base := rel("Acme news")
	if base != 0.2 {
		t.Fatalf("base relevance = %v, want 0.2", base)
	}
	if got := rel("Acme alpha beta gamma"); got != 0.38 {
		t.Errorf("three boosts: %v, want 0.38", got)
	}
	four, seven := rel("Acme alpha beta gamma delta"), rel("Acme alpha beta gamma delta epsilon zeta eta")
	if four != 0.4 || seven != 0.4 {
		t.Errorf("four boosts %v, seven boosts %v; both want 0.4 (cap +0.20)", four, seven)
	}
	if got := rel("Acme one two three four five six seven"); got != 0 {
		t.Errorf("seven demotes: %v, want 0", got)
	}
	capped := l.Score(model.Article{Title: "Acme alpha beta gamma delta epsilon"})
	if !contains(capped.Reasons, "keyword adjustment capped at +0.20") {
		t.Errorf("no cap reason in %q", capped.Reasons)
	}
	if got := rel("Acme alpha alpha alpha alpha alpha alpha"); got != 0.26 {
		t.Errorf("repeated word counts once: %v, want 0.26", got)
	}
}

func TestPhraseMatching(t *testing.T) {
	tests := []struct {
		text, phrase string
		want         bool
	}{
		{"strong moat here", "moat", true},
		{"a moated castle", "moat", false},
		{"the annual report is out", "annual report", true},
		{"the annual  reports are out", "annual report", false},
		{"filed 10-k405 today", "10-k", false},
		{"filed 10-k today", "10-k", true},
		{"new price targets issued", "price target", true},
		{"new price targets issued", "price targe", false},
		{"share repurchase announced", "buyback", true},
		{"moat", "moat", true},
		{"", "moat", false},
		{"moat", "", false},
	}
	for _, tc := range tests {
		if got := phraseFound(normalize(tc.text), normalize(tc.phrase)); got != tc.want {
			t.Errorf("phraseFound(%q, %q) = %v, want %v", tc.text, tc.phrase, got, tc.want)
		}
	}
}

func TestPhraseCaseInsensitiveFromConfig(t *testing.T) {
	l := New(config.Lens{Boost: []string{"  Free Cash FLOW "}}, Options{})
	if got := l.Score(model.Article{Title: "Free cash flow grows"}).Relevance; got != 0.26 {
		t.Errorf("relevance = %v, want 0.26", got)
	}
}

func TestHooks(t *testing.T) {
	art := model.Article{SourceID: "edgar", Title: "Acme opens new store"}
	if got := New(defaultCfg, Options{}).Score(art).Relevance; got != 0.2 {
		t.Errorf("nil hooks: %v, want 0.2", got)
	}
	if got := New(defaultCfg, testOpts()).Score(art).Relevance; got != 0.25 {
		t.Errorf("primary tier: %v, want 0.25", got)
	}
	art.SourceID = "blog"
	if got := New(defaultCfg, testOpts()).Score(art).Relevance; got != 0.15 {
		t.Errorf("commentary tier: %v, want 0.15", got)
	}
	art.SourceID = "wire"
	if got := New(defaultCfg, testOpts()).Score(art).Relevance; got != 0.2 {
		t.Errorf("reporting tier: %v, want 0.2", got)
	}
	filing := model.Article{Items: []string{"2.02"}, Title: "Acme news"}
	if got := New(defaultCfg, Options{}).Score(filing).Kind; got != model.EventOther {
		t.Errorf("nil ItemKind: kind %s, want other", got)
	}
	if got := New(defaultCfg, testOpts()).Score(filing).Kind; got != model.EventResults {
		t.Errorf("ItemKind set: kind %s, want results", got)
	}
}

func TestDefaultListsSamples(t *testing.T) {
	for _, opts := range []Options{{}, testOpts()} {
		l := New(defaultCfg, opts)
		buyback := l.Score(model.Article{SourceID: "wire", Title: "Company X announces $5 billion share repurchase program"})
		if buyback.Relevance < 0.5 {
			t.Errorf("buyback relevance = %v, want at least 0.5", buyback.Relevance)
		}
		target := l.Score(model.Article{SourceID: "wire", Title: "Analyst raises price target on X"})
		if target.Relevance >= 0.5 {
			t.Errorf("price target relevance = %v, want below 0.5", target.Relevance)
		}
	}
}

func TestVersion(t *testing.T) {
	shape := regexp.MustCompile(`^l0-1-[0-9a-f]{8}$`)
	base := New(config.Lens{Boost: []string{"moat", "buyback"}, Demote: []string{"meme stock"}}, Options{})
	if !shape.MatchString(base.Version()) {
		t.Fatalf("version %q has the wrong shape", base.Version())
	}
	reordered := New(config.Lens{Boost: []string{"buyback", "moat", "MOAT"}, Demote: []string{"meme stock"}}, Options{})
	if reordered.Version() != base.Version() {
		t.Errorf("reordered boost: %q, want %q", reordered.Version(), base.Version())
	}
	withHooks := New(config.Lens{Boost: []string{"moat", "buyback"}, Demote: []string{"meme stock"}}, testOpts())
	if withHooks.Version() != base.Version() {
		t.Errorf("hooks changed the version: %q, want %q", withHooks.Version(), base.Version())
	}
	changed := map[string]config.Lens{
		"boost word changed":   {Boost: []string{"moat", "buybacks"}, Demote: []string{"meme stock"}},
		"boost word removed":   {Boost: []string{"moat"}, Demote: []string{"meme stock"}},
		"demote word changed":  {Boost: []string{"moat", "buyback"}, Demote: []string{"meme stocks"}},
		"demote word added":    {Boost: []string{"moat", "buyback"}, Demote: []string{"meme stock", "intraday"}},
		"word moved to demote": {Boost: []string{"moat"}, Demote: []string{"meme stock", "buyback"}},
	}
	for name, cfg := range changed {
		if v := New(cfg, Options{}).Version(); v == base.Version() {
			t.Errorf("%s: version did not change (%s)", name, v)
		}
	}
	if got := base.Score(model.Article{Title: "x"}).LensVersion; got != base.Version() {
		t.Errorf("LensVersion = %q, want %q", got, base.Version())
	}
}

func TestVersionCoversRuleTables(t *testing.T) {
	before := digest(nil, nil)
	saved := rulesEncoding
	defer func() { rulesEncoding = saved }()
	rulesEncoding += "extra rule\n"
	if digest(nil, nil) == before {
		t.Error("digest ignores the rule tables")
	}
	for _, want := range []string{"weights", "kind", "magnitude", "routine", "noise", "alias", "form"} {
		if !strings.Contains(saved, want+" ") {
			t.Errorf("rule encoding lacks %q", want)
		}
	}
}

func TestDeterministic(t *testing.T) {
	l := New(defaultCfg, testOpts())
	a := model.Article{SourceID: "edgar", Form: "8-K", Items: []string{"2.02"},
		Title: "Acme record earnings, buyback and dividend", Summary: "Guidance in line with the moat"}
	first := l.Score(a)
	for range 20 {
		if got := l.Score(a); !reflect.DeepEqual(got, first) {
			t.Fatalf("score changed between calls:\n%+v\n%+v", got, first)
		}
	}
	if other := New(defaultCfg, testOpts()).Score(a); !reflect.DeepEqual(other, first) {
		t.Errorf("a second Lens disagrees:\n%+v\n%+v", other, first)
	}
}

func TestRelevantUsesThreshold(t *testing.T) {
	s := New(defaultCfg, Options{}).Score(model.Article{Title: "Company X announces $5 billion share repurchase program"})
	if !s.Relevant(defaultCfg.Threshold) {
		t.Errorf("relevance %v should reach threshold %v", s.Relevance, defaultCfg.Threshold)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
