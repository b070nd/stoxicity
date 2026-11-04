package classify

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/model"
)

var testTopics = []config.TopicRule{
	{Name: "ai-infrastructure", Words: []string{"data center", "GPU"}, Tickers: []string{"NVDA"}},
	{Name: "interest-rates", Words: []string{"rate cut", "interest rates", "Fed's decision"}},
	{Name: "housing", Words: []string{"mortgage rates", "home-builder"}},
	{Name: "ai", Words: []string{"ai"}},
}

var testSectors = []config.SectorRule{
	{Name: "technology", Words: []string{"software", "cloud computing"}, Tickers: []string{"MSFT", "NVDA"}},
	{Name: "financials", Words: []string{"net interest income", "lender"}, Tickers: []string{"JPM"}},
	{Name: "energy", Words: []string{"crude oil"}, Tickers: []string{"XOM"}},
}

func article(title, summary string, tickers ...string) model.Article {
	return model.Article{Title: title, Summary: summary, Tickers: tickers}
}

func TestClassifyTopics(t *testing.T) {
	c := New(testTopics, testSectors)
	tests := []struct {
		name string
		a    model.Article
		want []string
	}{
		{"nothing matches", article("Quarterly report published", ""), nil},
		{"word", article("Hyperscaler orders another data center", ""), []string{"ai-infrastructure"}},
		{"case insensitive", article("GPU SHORTAGE EASES", ""), []string{"ai-infrastructure"}},
		{"word in summary", article("Update", "Analysts expect a rate cut in March"), []string{"interest-rates"}},
		{"phrase must be contiguous", article("A data and center story", ""), nil},
		{"phrase across title and summary is not joined", article("A new data", "center opens"), []string{"ai-infrastructure"}},
		{"word boundary before", article("Karate cut prices", ""), nil},
		{"word boundary after", article("Interest rates", ""), []string{"interest-rates"}},
		{"plural is not the singular", article("Several rate cuts expected", ""), nil},
		{"short word needs boundaries", article("He said it was fair", ""), nil},
		{"short word matches as a word", article("AI demand lifts suppliers", ""), []string{"ai"}},
		{"short word next to punctuation", article("Apple, AI, and chips", ""), []string{"ai"}},
		{"curly apostrophe", article("Ahead of the Fed’s decision on Wednesday", ""), []string{"interest-rates"}},
		{"dash variants", article("Home‑builder sentiment rises", ""), []string{"housing"}},
		{"white space collapsed", article("Mortgage   rates\nfall", ""), []string{"housing"}},
		{"ticker triggers topic", article("Quarterly report published", "", "nvda"), []string{"ai-infrastructure"}},
		{"ticker in text is not enough", article("NVDA reports", ""), nil},
		{"word and ticker give one topic", article("GPU maker", "", "NVDA"), []string{"ai-infrastructure"}},
		{"several topics sorted", article("Mortgage rates and interest rates; GPU demand", ""), []string{"ai-infrastructure", "housing", "interest-rates"}},
		{"empty article", model.Article{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := c.Classify(tt.a)
			if !slices.Equal(got, tt.want) {
				t.Errorf("topics = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifySector(t *testing.T) {
	c := New(testTopics, testSectors)
	tests := []struct {
		name string
		a    model.Article
		want string
	}{
		{"no hit", article("Quarterly report published", ""), ""},
		{"one word", article("New software release", ""), "technology"},
		{"ticker", article("Quarterly report published", "", "XOM"), "energy"},
		{"words add up", article("Lender reports net interest income", ""), "financials"},
		{"repeated word counts once", article("Software software software; crude oil", ""), ""},
		{"ticker outweighs two words", article("Cloud computing software at a lender", "", "JPM"), "financials"},
		{"two words beat one", article("Cloud computing software firm and a lender", ""), "technology"},
		{"ticker beats word", article("Software and crude oil", "", "XOM"), "energy"},
		{"tie gives no sector", article("A lender and a software firm", ""), ""},
		{"tie of tickers gives no sector", article("Report", "", "XOM", "JPM"), ""},
		{"tie at the top only", article("Cloud computing software, a lender", ""), "technology"},
		{"unknown ticker", article("Report", "", "ZZZZ"), ""},
		{"lower-case ticker", article("Report", "", "xom"), "energy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := c.Classify(tt.a)
			if got != tt.want {
				t.Errorf("sector = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyIsDeterministic(t *testing.T) {
	a := article("GPU demand, mortgage rates, interest rates and software at a lender", "", "NVDA", "JPM")
	first, firstSector := New(testTopics, testSectors).Classify(a)
	for i := 0; i < 20; i++ {
		got, sector := New(testTopics, testSectors).Classify(a)
		if !slices.Equal(got, first) || sector != firstSector {
			t.Fatalf("run %d = %v %q, want %v %q", i, got, sector, first, firstSector)
		}
	}
	if !slices.IsSorted(first) {
		t.Errorf("topics %v are not sorted", first)
	}
}

func TestClassifyMergesRulesWithTheSameName(t *testing.T) {
	c := New(
		[]config.TopicRule{{Name: "a", Words: []string{"x"}}, {Name: "a", Words: []string{"y"}}},
		[]config.SectorRule{{Name: "s", Words: []string{"x"}}, {Name: "s", Words: []string{"y"}}},
	)
	topics, sector := c.Classify(article("x and y", ""))
	if !slices.Equal(topics, []string{"a"}) || sector != "s" {
		t.Errorf("Classify = %v %q, want [a] s", topics, sector)
	}
}

func TestNilClassifier(t *testing.T) {
	var c *Classifier
	topics, sector := c.Classify(article("GPU data center", "", "NVDA"))
	if topics != nil || sector != "" {
		t.Errorf("nil Classify = %v %q, want nothing", topics, sector)
	}
	if v := c.Version(); v != "" {
		t.Errorf("nil Version = %q, want empty", v)
	}
}

func TestEmptyClassifierClassifiesNothing(t *testing.T) {
	topics, sector := New(nil, nil).Classify(article("GPU data center", "", "NVDA"))
	if topics != nil || sector != "" {
		t.Errorf("Classify = %v %q, want nothing", topics, sector)
	}
}

func TestVersion(t *testing.T) {
	base := New(testTopics, testSectors).Version()
	if len(base) != len("c1-")+8 || !strings.HasPrefix(base, "c1-") {
		t.Fatalf("Version = %q, want c1- and 8 hex characters", base)
	}
	if again := New(testTopics, testSectors).Version(); again != base {
		t.Errorf("Version changed between identical classifiers: %q, %q", base, again)
	}

	reordered := New(
		[]config.TopicRule{testTopics[3], testTopics[2], testTopics[1], testTopics[0]},
		[]config.SectorRule{testSectors[2], testSectors[0], testSectors[1]},
	)
	if got := reordered.Version(); got != base {
		t.Errorf("reordered rules: Version = %q, want %q", got, base)
	}

	shuffledTerms := slices.Clone(testTopics)
	shuffledTerms[0] = config.TopicRule{Name: "ai-infrastructure", Words: []string{"gpu", "  DATA   center "}, Tickers: []string{"nvda"}}
	if got := New(shuffledTerms, testSectors).Version(); got != base {
		t.Errorf("reordered and re-cased words: Version = %q, want %q", got, base)
	}

	edit := func(f func(topics []config.TopicRule, sectors []config.SectorRule)) string {
		topics := make([]config.TopicRule, len(testTopics))
		for i, r := range testTopics {
			r.Words, r.Tickers = slices.Clone(r.Words), slices.Clone(r.Tickers)
			topics[i] = r
		}
		sectors := make([]config.SectorRule, len(testSectors))
		for i, r := range testSectors {
			r.Words, r.Tickers = slices.Clone(r.Words), slices.Clone(r.Tickers)
			sectors[i] = r
		}
		f(topics, sectors)
		return New(topics, sectors).Version()
	}
	changes := map[string]func([]config.TopicRule, []config.SectorRule){
		"topic word added":      func(t []config.TopicRule, _ []config.SectorRule) { t[0].Words = append(t[0].Words, "tpu") },
		"topic word removed":    func(t []config.TopicRule, _ []config.SectorRule) { t[0].Words = t[0].Words[:1] },
		"topic ticker added":    func(t []config.TopicRule, _ []config.SectorRule) { t[0].Tickers = append(t[0].Tickers, "AMD") },
		"topic renamed":         func(t []config.TopicRule, _ []config.SectorRule) { t[2].Name = "homes" },
		"topic dropped":         func(t []config.TopicRule, _ []config.SectorRule) { t[3].Words = nil },
		"sector word added":     func(_ []config.TopicRule, s []config.SectorRule) { s[0].Words = append(s[0].Words, "saas") },
		"sector ticker changed": func(_ []config.TopicRule, s []config.SectorRule) { s[2].Tickers = []string{"CVX"} },
		"word moved to sector":  func(t []config.TopicRule, s []config.SectorRule) { s[2].Words = append(s[2].Words, "gpu") },
	}
	for name, f := range changes {
		if got := edit(f); got == base {
			t.Errorf("%s: Version did not change from %q", name, base)
		}
	}

	if got := New([]config.TopicRule{{Name: "x", Words: []string{"y"}}}, nil).Version(); got == New(nil, []config.SectorRule{{Name: "x", Words: []string{"y"}}}).Version() {
		t.Errorf("a topic and a sector with the same rule share Version %q", got)
	}
}

// TestShippedConfig loads the repository's config.yaml and checks that it
// files realistic headlines. The headlines are written for this test.
func TestShippedConfig(t *testing.T) {
	for _, name := range []string{
		"STOXICITY_TIMEZONE", "STOXICITY_DATA_DIR", "STOXICITY_CONTACT", "STOXICITY_LAYA_URL", "STOXICITY_TENANT",
	} {
		t.Setenv(name, "")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// This file is app/src/classify/classify_test.go; the module root is app/.
	cfg, err := config.Load(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	c := New(cfg.Topics, cfg.Sectors)

	tests := []struct {
		a      model.Article
		topic  string
		sector string
	}{
		{article("Microsoft lifts data centre capex again as AI demand outstrips capacity", "", "MSFT"), "ai-infrastructure", "technology"},
		{article("Fed holds interest rates steady and signals one rate cut before year-end", "The Federal Reserve kept its benchmark rate unchanged."), "interest-rates", ""},
		{article("JPMorgan profit beats estimates as net interest income climbs", "Loan loss provisions fell for a second quarter.", "JPM"), "banks-and-credit", "financials"},
		{article("Eli Lilly shares jump after phase 3 trial of obesity drug meets main goal", "", "LLY"), "biotech", "health-care"},
		{article("Exxon raises quarterly dividend and extends share repurchase plan as crude oil climbs", "", "XOM"), "buybacks-and-dividends", "energy"},
		{article("US widens export controls on advanced chips; Beijing warns of retaliation", ""), "china-and-trade", ""},
		{article("Retail sales steady as mortgage rates ease and home prices cool", ""), "housing", ""},
	}
	for _, tt := range tests {
		topics, sector := c.Classify(tt.a)
		if len(topics) == 0 || !slices.Contains(topics, tt.topic) {
			t.Errorf("%q: topics = %v, want to include %q", tt.a.Title, topics, tt.topic)
		}
		if sector != tt.sector {
			t.Errorf("%q: sector = %q, want %q", tt.a.Title, sector, tt.sector)
		}
	}
}
