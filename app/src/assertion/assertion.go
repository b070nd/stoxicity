// Package assertion reads what an article claims, one claim at a time.
//
// An article is not one event. It can say that a company agreed to buy another,
// that it also raised its dividend, that a rumour of bankruptcy is false, and
// that last year it settled a lawsuit; the first keyword to match must not decide
// which of these the article is about. Extract splits the text into sentences
// and finds in each the claims it makes: the kind of event, which companies it
// is about and in what role (subject, counterparty, or only mentioned), whether
// the sentence negates or denies it, whether it is in the past, who vouches for
// it, and whether it is said to have happened, been announced, or only been
// proposed. Filings add claims of their own from their form and 8-K items.
//
// Assertions are observations about the text. What they mean for a security (how
// material, how new, how urgent) is a separate, versioned reading: see Assess.
// Nothing here uses a model; a model's proposals, if one is ever used, are checked
// against the text by Vet before they are kept.
package assertion

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Version names the extraction rules; ExtractorRules is the extractor that
// applies them.
const (
	Version        = "assert-1"
	ExtractorRules = "rules-1"
)

// Role is how a company stands to a claim.
type Role string

// The roles. A company that is only mentioned in a sentence has no part in the
// claim and gets no assessment from it.
const (
	Subject      Role = "subject"
	Counterparty Role = "counterparty"
	Mentioned    Role = "mentioned"
)

// Status says how far the claimed event has come.
type Status string

// The statuses.
const (
	Unknown   Status = "unknown"   // the sentence does not say
	Proposed  Status = "proposed"  // talks, plans, rumours, expectations
	Announced Status = "announced" // agreed, declared, approved; not yet done
	Completed Status = "completed" // done
)

// Attribution says who vouches for a claim.
type Attribution string

// The attributions.
const (
	Unattributed Attribution = ""
	OwnStatement Attribution = "own"      // the company's filing or statement
	Reported     Attribution = "reported" // according to a report, sources, or rumour
)

// Methods of finding a claim.
const (
	MethodText = "text"
	MethodItem = "filing-item"
	MethodForm = "filing-form"
)

// Party is a company a claim is about.
type Party struct {
	Ticker string
	Role   Role
	// Basis says how the company was tied to the claim: "named" (its name or
	// symbol is in the sentence), "filer" (the filing is its own) or "sole
	// ticker" (the article has no other company, and the sentence does not name
	// one).
	Basis string
}

// Assertion is one claim found in an article.
type Assertion struct {
	Seq    int
	Kind   model.EventKind
	Method string
	// Item is the 8-K item a filing claim comes from.
	Item string
	// Span is the sentence the claim is in, verbatim, and Start and End its
	// byte offsets in Title + "\n" + Summary. Match is the word that gave the
	// kind.
	Span       string
	Start, End int
	Match      string
	Parties    []Party

	Negated    bool // a denial word stands before the keyword: "no restatement"
	Denied     bool // the sentence denies or disputes a claim: "denies the report"
	Historical bool // the claim is about the past, not a new event
	// Anticipatory says the sentence looks ahead to an event ("ahead of
	// earnings"): the event is the subject of a preview, not news.
	Anticipatory bool
	Status       Status
	Attribution  Attribution
	// Magnitude and Routine are phrases of the sentence that make the event
	// bigger or smaller than its kind alone.
	Magnitude, Routine string
	// Rated is the materiality a filing's item states for itself, 0 when the
	// claim is read from text. It rates the kind of filing, not an amount: a
	// filing item is evidence that something happened, not proof of its size.
	Rated int
}

// Live says the assertion claims a current event: it is not negated, denied,
// historical or a preview.
func (a Assertion) Live() bool {
	return !a.Negated && !a.Denied && !a.Historical && !a.Anticipatory
}

// Party returns the assertion's party for the ticker.
func (a Assertion) Party(ticker string) (Party, bool) {
	for _, p := range a.Parties {
		if strings.EqualFold(p.Ticker, ticker) {
			return p, true
		}
	}
	return Party{}, false
}

// Input is what is read of an article.
type Input struct {
	ArticleID      string
	Title, Summary string
	Form           string
	Items          []string
	// Tickers are the companies the article was tied to; for a filing, the
	// filer's.
	Tickers   []string
	Published time.Time
	// Score is the lens's reading of the article, used for the kind of a filing
	// that has no items (a Form 4, a 10-K).
	Score model.Score
	// Names gives the names a company is written by; nil means only symbols and
	// the article's sole ticker tie a sentence to a company.
	Names func(ticker string) []string
	// ItemRating reads the 8-K items; nil leaves them without a rating.
	ItemRating func(items []string) (model.ItemRating, bool)
	// Truncated says the text was cut before it was read.
	Truncated bool
}

// Result is the claims of one article, with how they were found.
type Result struct {
	ArticleID  string
	Assertions []Assertion
	Version    string
	Extractor  string
	// Model and PromptVersion name the model and prompt that proposed the claims
	// when one did; both are empty for the rules.
	Model, PromptVersion string
	Truncated            bool
}

// Extract finds the claims of an article. It reads the text as data: nothing in
// it can change how it is read.
func Extract(in Input) Result {
	res := Result{ArticleID: in.ArticleID, Version: Version, Extractor: ExtractorRules, Truncated: in.Truncated}
	text := in.Title + "\n" + in.Summary
	filer := in.Form != "" || len(in.Items) > 0

	if filer {
		res.Assertions = append(res.Assertions, filingClaims(in)...)
	}
	for _, s := range sentences(text) {
		for _, m := range lens.KindMatches(s.text) {
			if filer && m.Kind != model.EventOther && alreadyHas(res.Assertions, m.Kind) {
				continue // the filing's own item says this; its prose is not a second claim
			}
			a := Assertion{Kind: m.Kind, Method: MethodText, Span: s.text, Start: s.start, End: s.end, Match: m.Text}
			readContext(&a, s.text, m, in.Published)
			a.Parties = parties(s.text, a.Kind, in)
			res.Assertions = append(res.Assertions, a)
		}
	}
	for i := range res.Assertions {
		res.Assertions[i].Seq = i + 1
	}
	return res
}

func alreadyHas(as []Assertion, k model.EventKind) bool {
	return slices.ContainsFunc(as, func(a Assertion) bool { return a.Method != MethodText && a.Kind == k })
}

// filingClaims are the claims a filing makes by being what it is: one for each
// 8-K item, or one for its form.
func filingClaims(in Input) []Assertion {
	var out []Assertion
	parties := make([]Party, 0, len(in.Tickers))
	for _, t := range in.Tickers {
		parties = append(parties, Party{Ticker: t, Role: Subject, Basis: "filer"})
	}
	span := strings.TrimSpace(in.Title)
	for _, item := range in.Items {
		if in.ItemRating == nil {
			continue
		}
		r, ok := in.ItemRating([]string{item})
		if !ok || !r.Kind.Valid() {
			continue
		}
		out = append(out, Assertion{Kind: r.Kind, Method: MethodItem, Item: item, Span: span, Start: 0, End: len(in.Title),
			Match: "8-K item " + item, Parties: slices.Clone(parties), Status: Announced, Attribution: OwnStatement, Rated: r.Materiality})
	}
	if len(out) == 0 && in.Score.Kind != "" && in.Score.Kind != model.EventOther {
		out = append(out, Assertion{Kind: in.Score.Kind, Method: MethodForm, Span: span, Start: 0, End: len(in.Title),
			Match: "form " + strings.ToUpper(in.Form), Parties: slices.Clone(parties), Status: Completed, Attribution: OwnStatement})
	}
	return out
}

// sentence is a piece of the text with its place in it.
type sentence struct {
	text       string
	start, end int
}

// abbreviations end in a full stop without ending the sentence.
var abbreviations = map[string]bool{
	"inc": true, "corp": true, "co": true, "ltd": true, "plc": true, "llc": true, "no": true, "mr": true, "ms": true, "mrs": true, "dr": true,
	"st": true, "jr": true, "sr": true, "vs": true, "u.s": true, "u.k": true, "e.g": true, "i.e": true, "etc": true, "jan": true, "feb": true,
	"mar": true, "apr": true, "jun": true, "jul": true, "aug": true, "sep": true, "sept": true, "oct": true, "nov": true, "dec": true, "approx": true,
}

// sentences cuts the text at line ends and at a full stop, question mark or
// exclamation mark followed by a space and the start of a new sentence.
func sentences(text string) []sentence {
	var out []sentence
	add := func(from, to int) {
		seg := text[from:to]
		trimmed := strings.TrimLeftFunc(seg, unicode.IsSpace)
		from += len(seg) - len(trimmed)
		trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace)
		if trimmed != "" {
			out = append(out, sentence{trimmed, from, from + len(trimmed)})
		}
	}
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\n':
			add(start, i)
			start = i + 1
		case (c == '.' || c == '!' || c == '?') && i+1 < len(text) && (text[i+1] == ' ' || text[i+1] == '\t'):
			j := i + 1
			for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
				j++
			}
			if j >= len(text) {
				continue
			}
			next := rune(text[j])
			if !(unicode.IsUpper(next) || unicode.IsDigit(next) || next == '"' || next == '\'' || next == '(') {
				continue
			}
			if c == '.' {
				k := i
				for k > start && (unicode.IsLetter(rune(text[k-1])) || text[k-1] == '.') {
					k--
				}
				if abbreviations[strings.ToLower(text[k:i])] {
					continue
				}
			}
			add(start, i+1)
			start = j
			i = j - 1
		}
	}
	add(start, len(text))
	return out
}

var (
	reHistorical = regexp.MustCompile(`\b(?:last (?:year|quarter|month|decade)|years? ago|a year ago|previously|formerly|in the past|earlier this year|back in)\b`)
	reYear       = regexp.MustCompile(`\b(?:in|during|since|from|of|fiscal)\s+(?:(?:early|late|mid)[- ])?(?:(?:january|february|march|april|may|june|july|august|september|october|november|december)\s+)?((?:19|20)\d\d)\b`)
	reDenied     = regexp.MustCompile(`\b(?:denies|denied|deny|denying|rejects?|rejected|refutes?|refuted|disputes?|disputed|dismisses|dismissed|baseless|unfounded|false claims?|false report)\b`)
	// reRetracted marks a sentence that restates an earlier story only to withdraw
	// it ("An earlier version of this story said ..."): the claim it repeats is not
	// a new event, so it is recorded as denied.
	reRetracted = regexp.MustCompile(`\b(?:(?:an? )?(?:earlier|previous|prior) (?:version|report|story|article|headline) (?:of this (?:story|article|report) )?(?:said|stated|reported|incorrectly|erroneously|misstated)|erroneously|incorrectly (?:said|stated|reported)|in error|was (?:incorrect|wrong|mistaken)|has been corrected|(?:is|was) retracted|retracts?|retraction|misstated)\b`)
	reReported  = regexp.MustCompile(`\b(?:according to|reportedly|reports? (?:say|said|that)|sources?|people familiar|familiar with the matter|rumou?rs?|speculat\w+|unconfirmed|is said to|are said to)\b`)
	reOwn       = regexp.MustCompile(`\b(?:the company (?:said|says|announced|announces|stated|reported)|said in a (?:statement|filing|release)|in a press release|press release|filed with the sec|said in an? 8-k)\b`)
	reAhead     = regexp.MustCompile(`\b(?:ahead of|ahead|before|previews?|awaits?|awaiting|in advance of|upcoming|next (?:week|month|quarter))\b`)
	reProposed  = regexp.MustCompile(`\b(?:plans? to|planning to|intends? to|considering|exploring|in talks|talks to|may|might|could|would|seeks? to|weighs|weighing|proposes?|proposed|proposal|bids? for|offers? to|rumou?r\w*|expected to|set to|poised to|will|is to|are to|looking to|eyes|nears)\b`)
	reAnnounced = regexp.MustCompile(`\b(?:agreed to|agrees to|signed|definitive agreement|entered into|announces?|announced|declares?|declared|approves?|approved|authori[sz]\w+)\b`)
	reCompleted = regexp.MustCompile(`\b(?:completed|completes|closed|closes|finali[sz]ed|has (?:acquired|bought|sold)|resigned|resigns|filed|files)\b`)
	reBeat      = regexp.MustCompile(`\bahead of (?:the )?(?:expectations?|estimates?|forecasts?|consensus|schedule|analysts?|street|guidance|plan)\b`)
	reClause    = regexp.MustCompile(`[,;:]| - | \(`)
)

// readContext fills what the sentence says about the claim: negation, denial,
// time, status and attribution.
func readContext(a *Assertion, raw string, m lens.KindMatch, published time.Time) {
	norm := lens.Normalized(raw)
	a.Negated = m.Denied
	a.Denied = reDenied.MatchString(norm) || reRetracted.MatchString(norm)
	a.Historical = historical(norm, published)
	before := norm[:min(m.At, len(norm))]
	if loc := reClause.FindAllStringIndex(before, -1); len(loc) > 0 {
		before = before[loc[len(loc)-1][1]:] // only the clause the keyword is in
	}
	// The status is what stands nearest before the keyword.
	best, at := Unknown, -1
	for _, c := range []struct {
		re *regexp.Regexp
		st Status
	}{{reProposed, Proposed}, {reAnnounced, Announced}, {reCompleted, Completed}} {
		if locs := c.re.FindAllStringIndex(before, -1); len(locs) > 0 && locs[len(locs)-1][0] > at {
			best, at = c.st, locs[len(locs)-1][0]
		}
	}
	after := ""
	if end := m.At + len(m.Text); end < len(norm) {
		after = norm[end:]
		if loc := reClause.FindStringIndex(after); loc != nil {
			after = after[:loc[0]]
		}
	}
	// "earnings ahead of the report" looks ahead; "earnings ahead of estimates" does not.
	if reAhead.MatchString(before) || (reAhead.MatchString(after) && !reBeat.MatchString(after)) {
		a.Anticipatory = true
		if best == Unknown {
			best = Proposed
		}
	}
	a.Status = best
	switch {
	case reReported.MatchString(norm):
		a.Attribution = Reported
	case reOwn.MatchString(norm):
		a.Attribution = OwnStatement
	}
	a.Magnitude = lens.Magnitude(raw)
	if !lens.Noise(a.Kind) {
		a.Routine = lens.Routine(raw)
	}
}

func historical(norm string, published time.Time) bool {
	if reHistorical.MatchString(norm) {
		return true
	}
	if published.IsZero() {
		return false
	}
	for _, m := range reYear.FindAllStringSubmatch(norm, -1) {
		var y int
		fmt.Sscanf(m[1], "%d", &y)
		if y > 0 && y < published.Year() {
			return true
		}
	}
	return false
}

var (
	rePassive = regexp.MustCompile(`\b(?:to be|being|was|were|is|are|been)\s+(?:\w+\s+)?(?:acquired|bought|purchased|sued|taken over|bought out)\s+by\b`)
	// relational kinds have two sides.
	relational = map[model.EventKind]bool{
		model.EventAcquisition: true, model.EventAgreement: true, model.EventSpinoff: true, model.EventLitigation: true,
		model.EventControlChange: true, model.EventIssuance: true, model.Event13DG: true,
	}
	suffixes = regexp.MustCompile(`(?i)[,.]?\s+(?:inc|corp|corporation|co|company|ltd|limited|plc|llc|holdings|group|incorporated|n\.v|s\.a|ag|sa|nv)\.?$`)
	classTag = regexp.MustCompile(`(?i)\s+-\s+.*$|\s+(?:class [a-z]|common stock|ordinary shares|american depositary shares)\b.*$`)
)

// parties finds the companies a sentence is about and their roles.
func parties(raw string, kind model.EventKind, in Input) []Party {
	type hit struct {
		ticker string
		at     int
	}
	lower := strings.ToLower(raw)
	var hits []hit
	for _, t := range in.Tickers {
		at := -1
		if i := tickerAt(raw, t); i >= 0 {
			at = i
		}
		if in.Names != nil {
			for _, n := range names(in.Names(t)) {
				if i := phraseAt(lower, n); i >= 0 && (at < 0 || i < at) {
					at = i
				}
			}
		}
		if at >= 0 {
			hits = append(hits, hit{t, at})
		}
	}
	basis := "named"
	if len(hits) == 0 && len(in.Tickers) == 1 {
		hits, basis = []hit{{in.Tickers[0], 0}}, "sole ticker"
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.at - b.at })
	if len(hits) == 0 {
		return nil
	}
	subject := 0
	if rePassive.MatchString(lens.Normalized(raw)) && len(hits) > 1 {
		subject = len(hits) - 1 // "Bolt is to be acquired by Acme": the party after "by" acts
	}
	out := make([]Party, len(hits))
	for i, h := range hits {
		role := Mentioned
		switch {
		case i == subject:
			role = Subject
		case relational[kind]:
			role = Counterparty
		}
		out[i] = Party{Ticker: h.ticker, Role: role, Basis: basis}
	}
	return out
}

// tickerAt finds the symbol as a whole upper-case word, or for a one-letter
// symbol only in the form "(NYSE: A)".
func tickerAt(raw, ticker string) int {
	t := strings.ToUpper(strings.TrimSpace(ticker))
	if t == "" {
		return -1
	}
	re := regexp.MustCompile(`(^|[^A-Za-z0-9.\-])` + regexp.QuoteMeta(t) + `($|[^A-Za-z0-9\-])`)
	if len(t) == 1 {
		re = regexp.MustCompile(`[:(]\s*` + regexp.QuoteMeta(t) + `\b`)
	}
	loc := re.FindStringIndex(raw)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// names turns the names a company is written by into the lower-case phrases a
// sentence is searched for: the name, and the name without its legal suffix.
func names(in []string) []string {
	var out []string
	for _, n := range in {
		n = strings.ToLower(strings.TrimSpace(classTag.ReplaceAllString(n, "")))
		if len(n) < 4 {
			continue
		}
		out = append(out, n)
		if s := strings.TrimSpace(suffixes.ReplaceAllString(n, "")); len(s) >= 4 && s != n {
			out = append(out, s)
		}
	}
	return out
}

func phraseAt(lower, phrase string) int {
	for from := 0; from+len(phrase) <= len(lower); {
		i := strings.Index(lower[from:], phrase)
		if i < 0 {
			return -1
		}
		i += from
		end := i + len(phrase)
		okB := i == 0 || !isAlnum(rune(lower[i-1]))
		okA := end == len(lower) || !isAlnum(rune(lower[end]))
		if okB && okA {
			return i
		}
		from = i + 1
	}
	return -1
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
