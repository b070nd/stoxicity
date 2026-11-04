package entity

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

// testEntities is a small synthetic table; it is not real listing data.
func testEntities() []model.Entity {
	return []model.Entity{
		{Ticker: "AAPL", Name: "Apple Inc."},
		{Ticker: "", Name: "Ignored Co"},
		{Ticker: "AAPL", Name: "Duplicate Ticker Inc."},
		{Ticker: "MSFT", Name: "Microsoft Corporation"},
		{Ticker: "KO", Name: "The Coca-Cola Company"},
		{Ticker: "TGT", Name: "Target Corporation"},
		{Ticker: "IT", Name: "Gartner, Inc."},
		{Ticker: "ON", Name: "ON Semiconductor Corporation"},
		{Ticker: "ALL", Name: "The Allstate Corporation"},
		{Ticker: "GOOGL", Name: "Alphabet Inc.", Aliases: []string{"Google"}},
		{Ticker: "BRK-B", Name: "Berkshire Hathaway Inc."},
		{Ticker: "V", Name: "Visa Inc."},
		{Ticker: "JNJ", Name: "Johnson & Johnson"},
		{Ticker: "NESN", Name: "Nestlé S.A."},
		{Ticker: "GM", Name: "General Motors Company"},
		{Ticker: "IBM", Name: "International Business Machines Corporation", Aliases: []string{"Big Blue", "IBM"}},
		{Ticker: "AMZN", Name: "Amazon.com, Inc.", Aliases: []string{"Amazon"}},
		{Ticker: "APLE", Name: "Apple Hospitality REIT, Inc."},
		{Ticker: "GE", Name: "General Electric Company", Aliases: []string{"GE"}},
	}
}

func testIndex() *Index { return New(testEntities()) }

func tickersOf(es []model.Entity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Ticker
	}
	return out
}

func TestFind(t *testing.T) {
	ix := testIndex()
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"empty text", "", []string{}},
		{"no companies", "The weather was mild and nothing happened.", []string{}},
		{"cashtag", "Traders piled into $AAPL today", []string{"AAPL"}},
		{"cashtag of dotted ticker", "Long $BRK.B and $BRK-B", []string{"BRK-B"}},
		{"cashtag stoplisted", "Bought $IT, $ON and $ALL", []string{"IT", "ON", "ALL"}},
		{"cashtag unknown", "Nothing on $ZZZZ here", []string{}},
		{"cashtag glued to a word", "US$AAPL is not a cashtag", []string{}},
		{"exchange parenthesised", "Coca-Cola (NYSE: KO) reported", []string{"KO"}},
		{"exchange no space", "Microsoft (NASDAQ:MSFT) said", []string{"MSFT"}},
		{"exchange unparenthesised", "Shares of NYSE:KO rose", []string{"KO"}},
		{"exchange two words", "Gartner (NYSE American: IT) said", []string{"IT"}},
		{"exchange stoplisted ticker", "(NYSE: ON) and (NYSE: ALL)", []string{"ON", "ALL"}},
		{"exchange unknown ticker", "(NASDAQ: ZZZZ)", []string{}},
		{"exchange name in lower case is not an exchange", "a bit: AAPL", []string{}},
		{"name", "Microsoft announced a new product.", []string{"MSFT"}},
		{"name with suffix in text", "Microsoft Corporation announced a new product.", []string{"MSFT"}},
		{"leading The dropped", "Coca-Cola announced a new product.", []string{"KO"}},
		{"multi word name", "Johnson & Johnson announced a new product.", []string{"JNJ"}},
		{"name with dot", "Amazon.com announced a new product.", []string{"AMZN"}},
		{"apple fruit rejected", "The apple harvest was strong.", []string{}},
		{"capitalised Apple without context rejected", "Apple harvest was strong.", []string{}},
		{"Apple with finance context", "Apple shares rose after earnings.", []string{"AAPL"}},
		{"Apple with suffix", "Apple Inc. announced a new product.", []string{"AAPL"}},
		{"Apple with ticker elsewhere", "Apple was in the news. AAPL moved.", []string{"AAPL"}},
		{"Apple with CEO", "Apple CEO spoke.", []string{"AAPL"}},
		{"Apple with 10-K", "Apple filed its 10-K.", []string{"AAPL"}},
		{"context in another sentence does not count", "Shares fell. Apple harvest was strong.", []string{}},
		{"Apple Hospitality is not Apple Inc", "Apple Hospitality REIT shares rose.", []string{"APLE"}},
		{"Target without context rejected", "Target the weak spots first.", []string{}},
		{"Target with context", "Target reported quarterly revenue.", []string{"TGT"}},
		{"General alone is not General Motors", "General shares were mixed, the General said.", []string{}},
		{"General Motors in full", "General Motors recalled cars.", []string{"GM"}},
		{"General with exchange form", "General (NYSE: GM) said.", []string{"GM"}},
		{"alias", "Google unveiled a model.", []string{"GOOGL"}},
		{"alias multi word", "Big Blue unveiled a model.", []string{"IBM"}},
		{"short alias needs context", "GE unveiled a model. GE shares rose.", []string{"GE"}},
		{"short alias without context", "GE unveiled a model.", []string{}},
		{"two letter name without context", "ON Semiconductor unveiled a chip.", []string{"ON"}},
		{"case sensitive name", "MICROSOFT and microsoft and Microsoft's", []string{"MSFT"}},
		{"case sensitive name lower only", "microsoft announced", []string{}},
		{"case sensitive upper only", "MICROSOFT announced", []string{}},
		{"unicode name", "Shares of Nestlé rose, Nestlé said", []string{"NESN"}},
		{"bare ticker with context", "AAPL shares rose.", []string{"AAPL"}},
		{"bare ticker without context", "AAPL is a word here.", []string{}},
		{"bare ticker context other sentence", "Shares rose. AAPL moved.", []string{}},
		{"bare ticker in parentheses", "Apple (AAPL) stock fell", []string{"AAPL"}},
		{"bare ticker sentence end", "Investors bought shares of AAPL. Nothing else", []string{"AAPL"}},
		{"bare ticker possessive", "AAPL's shares rose", []string{"AAPL"}},
		{"bare ticker inside word", "XAAPLX shares and AAPLX stock", []string{}},
		{"bare ticker lower case", "aapl shares rose", []string{}},
		{"bare stoplisted tickers", "IT ON ALL shares rose, ARE stock now", []string{}},
		{"bare single letter ticker", "V shares rose", []string{}},
		{"bare ticker one sentence per newline", "AAPL\nshares rose", []string{}},
		{"two entities by appearance", "Microsoft shares rose while $AAPL fell.", []string{"MSFT", "AAPL"}},
		{"two entities reverse", "$AAPL fell while Microsoft shares rose.", []string{"AAPL", "MSFT"}},
		{"entity once even if matched many ways", "Microsoft (NASDAQ: MSFT) $MSFT MSFT shares", []string{"MSFT"}},
		{"unicode around", "株価 日本 — Microsoft…rose “Microsoft” ünïcode$AAPL", []string{"MSFT"}},
		{"name next to unicode letter is not whole word", "ÉMicrosoft rose", []string{}},
		{"name followed by digit is not whole word", "Microsoft2 rose", []string{}},
		{"hyphen boundary", "Microsoft-owned LinkedIn", []string{"MSFT"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tickersOf(ix.Find(tt.text))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Find(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestMatchesEvidenceAndOffsets(t *testing.T) {
	ix := testIndex()
	tests := []struct {
		name         string
		text         string
		wantTicker   string
		wantEvidence Evidence
		wantSpan     string
	}{
		{"cashtag", "buy $AAPL now", "AAPL", EvidenceCashtag, "$AAPL"},
		{"cashtag dotted", "buy $BRK.B now", "BRK-B", EvidenceCashtag, "$BRK.B"},
		{"cashtag before period", "buy $AAPL.", "AAPL", EvidenceCashtag, "$AAPL"},
		{"exchange parenthesised", "Apple (NASDAQ: AAPL) said", "AAPL", EvidenceExchange, "NASDAQ: AAPL"},
		{"exchange compact", "x (NYSE:KO) y", "KO", EvidenceExchange, "NYSE:KO"},
		{"exchange two words", "x (NYSE American: IT) y", "IT", EvidenceExchange, "NYSE American: IT"},
		{"name", "Today Microsoft said", "MSFT", EvidenceName, "Microsoft"},
		{"name multi word", "Today Johnson & Johnson said", "JNJ", EvidenceName, "Johnson & Johnson"},
		{"name with dot", "Today Amazon.com said", "AMZN", EvidenceName, "Amazon.com"},
		{"name with hyphen", "Today Coca-Cola said", "KO", EvidenceName, "Coca-Cola"},
		{"bare ticker paren", "Shares of (AAPL) rose", "AAPL", EvidenceTicker, "AAPL"},
		{"bare ticker possessive", "AAPL's shares rose", "AAPL", EvidenceTicker, "AAPL"},
		{"bare ticker period", "Buy shares of AAPL.", "AAPL", EvidenceTicker, "AAPL"},
		{"after multibyte text", "日本の株価 — Microsoft said", "MSFT", EvidenceName, "Microsoft"},
		{"strongest wins", "Apple shares rose. Later (NASDAQ: AAPL) and $AAPL", "AAPL", EvidenceCashtag, "$AAPL"},
		{"earliest of equal", "Microsoft said, then Microsoft said again", "MSFT", EvidenceName, "Microsoft"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := ix.Matches(tt.text)
			if len(ms) != 1 {
				t.Fatalf("Matches(%q) = %d matches, want 1: %+v", tt.text, len(ms), ms)
			}
			m := ms[0]
			if m.Entity.Ticker != tt.wantTicker || m.Evidence != tt.wantEvidence {
				t.Errorf("got %s/%v, want %s/%v", m.Entity.Ticker, m.Evidence, tt.wantTicker, tt.wantEvidence)
			}
			if got := tt.text[m.Start:m.End]; got != tt.wantSpan {
				t.Errorf("span = %q, want %q", got, tt.wantSpan)
			}
		})
	}
	if m := ix.Matches("Microsoft said, then Microsoft said again")[0]; m.Start != strings.Index("Microsoft said, then Microsoft said again", "Microsoft") {
		t.Errorf("earliest equal evidence not chosen, Start = %d", m.Start)
	}
}

func TestMatchesOrderFollowsFirstAppearance(t *testing.T) {
	ix := testIndex()
	text := "Microsoft shares rose. Meanwhile $AAPL fell and (NYSE: KO) held, and $MSFT again."
	ms := ix.Matches(text)
	var got []string
	for _, m := range ms {
		got = append(got, m.Entity.Ticker)
	}
	if want := []string{"MSFT", "AAPL", "KO"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if ms[0].Evidence != EvidenceCashtag {
		t.Errorf("MSFT evidence = %v, want cashtag", ms[0].Evidence)
	}
}

func TestLookup(t *testing.T) {
	ix := testIndex()
	tests := []struct {
		ticker string
		name   string
		ok     bool
	}{
		{"AAPL", "Apple Inc.", true},
		{"aapl", "Apple Inc.", true},
		{"BRK-B", "Berkshire Hathaway Inc.", true},
		{"brk-b", "Berkshire Hathaway Inc.", true},
		{"", "", false},
		{"NOPE", "", false},
	}
	for _, tt := range tests {
		e, ok := ix.Lookup(tt.ticker)
		if ok != tt.ok || e.Name != tt.name {
			t.Errorf("Lookup(%q) = %q, %v; want %q, %v", tt.ticker, e.Name, ok, tt.name, tt.ok)
		}
	}
}

func TestNewIgnoresEmptyAndDuplicateTickers(t *testing.T) {
	ix := testIndex()
	if e, _ := ix.Lookup("AAPL"); e.Name != "Apple Inc." {
		t.Errorf("duplicate ticker kept %q, want the first entity", e.Name)
	}
	if got := ix.Find("Ignored Co announced; Duplicate Ticker Inc. announced"); len(got) != 0 {
		t.Errorf("ignored entities were found: %v", got)
	}
	if got := New(nil).Find("Apple shares rose"); got != nil {
		t.Errorf("empty index found %v", got)
	}
}

func TestNormalisedWords(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{"Apple Inc.", []string{"Apple"}},
		{"The Coca-Cola Company", []string{"Coca", "Cola"}},
		{"Nestlé S.A.", []string{"Nestlé"}},
		{"Shell plc", []string{"Shell"}},
		{"Foo Holdings Group Ltd", []string{"Foo"}},
		{"Group Inc", []string{"Group"}},
		{"Procter & Gamble Co", []string{"Procter", "Gamble"}},
		{"Carnival Corp & plc", []string{"Carnival"}},
		{"Foo S.p.A.", []string{"Foo"}},
		{"Foo L.L.C.", []string{"Foo"}},
		{"AT&T Inc.", []string{"AT", "T"}},
		{"", []string{}},
	}
	for _, tt := range tests {
		got := normalisedWords(tt.name)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("normalisedWords(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSentenceBoundaries(t *testing.T) {
	ix := testIndex()
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"abbreviation dot keeps context", "Apple Inc. shares rose", []string{"AAPL"}},
		{"decimal keeps context", "Apple rose 3.5% on earnings", []string{"AAPL"}},
		{"question mark splits", "Why did shares fall? Apple harvest.", []string{}},
		{"exclamation splits", "Shares rose! Apple harvest.", []string{}},
		{"newline splits", "Shares rose\nApple harvest", []string{}},
		{"full stop splits", "Shares rose. Apple harvest", []string{}},
		{"quote after full stop", "He said \"shares rose.\" Apple harvest", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tickersOf(ix.Find(tt.text))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Find(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestWordLists(t *testing.T) {
	if len(ambiguousWords) < 60 {
		t.Errorf("ambiguousWords has %d entries, want at least 60", len(ambiguousWords))
	}
	if len(tickerStoplist) < 80 {
		t.Errorf("tickerStoplist has %d entries, want at least 80", len(tickerStoplist))
	}
	for _, w := range []string{"apple", "target", "visa", "block", "square", "amazon", "shell", "ford", "gap", "oracle"} {
		if _, ok := ambiguousWords[w]; !ok {
			t.Errorf("ambiguousWords lacks %q", w)
		}
	}
	for _, w := range []string{"A", "I", "IT", "ON", "ALL", "ARE", "KEY", "NOW", "FOR", "CEO", "CFO", "USA", "SEC", "ETF", "IPO", "GDP", "EPS", "YOY"} {
		if _, ok := tickerStoplist[w]; !ok {
			t.Errorf("tickerStoplist lacks %q", w)
		}
	}
	for w := range ambiguousWords {
		if w != strings.ToLower(w) {
			t.Errorf("ambiguousWords entry %q is not lower case", w)
		}
	}
	for w := range tickerStoplist {
		if w != strings.ToUpper(w) {
			t.Errorf("tickerStoplist entry %q is not upper case", w)
		}
	}
}

func TestEvidenceOrderAndString(t *testing.T) {
	if !(EvidenceCashtag > EvidenceExchange && EvidenceExchange > EvidenceName && EvidenceName > EvidenceTicker && EvidenceTicker > 0) {
		t.Error("evidence is not ranked cashtag > exchange > name > ticker")
	}
	for ev, want := range map[Evidence]string{EvidenceCashtag: "cashtag", EvidenceExchange: "exchange", EvidenceName: "name", EvidenceTicker: "ticker", 0: "none"} {
		if ev.String() != want {
			t.Errorf("Evidence(%d).String() = %q, want %q", ev, ev.String(), want)
		}
	}
}

func TestResolverInterface(t *testing.T) {
	var r Resolver = testIndex()
	if got := r.Find("Microsoft rose"); len(got) != 1 {
		t.Errorf("Resolver.Find = %v", got)
	}
}

func syntheticEntities(n int) []model.Entity {
	syl := []string{"ka", "lo", "mi", "ren", "tu", "va", "zor", "bel", "dax", "fin", "gor", "hal"}
	out := make([]model.Entity, n)
	for i := range out {
		a, b, c := syl[i%12], syl[(i/12)%12], syl[(i/144)%12]
		name := strings.ToUpper(a[:1]) + a[1:] + b + c
		out[i] = model.Entity{
			Ticker:  fmt.Sprintf("%c%c%c%c", 'A'+i%26, 'A'+(i/26)%26, 'A'+(i/676)%26, 'A'+(i/17576)%26),
			Name:    name + " " + strings.ToUpper(c[:1]) + c[1:] + "ex Holdings Inc.",
			Aliases: []string{name + "co"},
		}
	}
	return out
}

func syntheticText(n int) string {
	para := "Shares of Kalomi Holdings rose after earnings, while (NASDAQ: AAPL) and $MSFT slipped. " +
		"The apple harvest at Target farms was strong; analysts at ABCD said revenue guidance for the quarter " +
		"was ahead of the CEO's plan, and Zorfin Hal Inc. filed its 10-K. Nothing else happened in the market today. "
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(para)
	}
	return b.String()[:n]
}

func BenchmarkMatches(b *testing.B) {
	ents := syntheticEntities(10000)
	ents = append(ents, testEntities()...)
	ix := New(ents)
	text := syntheticText(2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.Matches(text)
	}
}

func BenchmarkNew(b *testing.B) {
	ents := syntheticEntities(10000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		New(ents)
	}
}
