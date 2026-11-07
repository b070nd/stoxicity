// Package lens scores an article the way a Peter Lynch / Warren Buffett style
// long-term investor would read it: business fundamentals, earnings,
// competitive moat, capital allocation and insider behaviour count; price
// targets, intraday moves and hype do not.
//
// This is stage L0: deterministic, explainable, no model. Its relevance is an
// UNCALIBRATED heuristic in [0,1]. The weights below are judgement, not fitted
// to labels, so a score of 0.6 does not mean a 60% chance of anything. Later
// stages (a learned scorer, then calibration) replace it.
//
// # Formula
//
// 1. Kind. When the article carries 8-K items and Options.ItemRating knows
// them, the most material item gives the kind and the materiality (reason
// "8-K item <n>"); Options.ItemKind alone gives only the kind. Otherwise a Form 4 filing is an
// insider_buy if its title has a buy or purchase word, else an insider_sell if
// it has a sale or sell word, else other; a 10-K or 10-Q is results; a 13D or
// 13G filing is 13d_13g. Anything else is read from the text: an ordered table
// of word-boundary, case-insensitive patterns is tried top to bottom and the
// first rule that matches wins; no match means other. The patterns are in
// kindRules.
//
// 2. Materiality starts at Kind.BaseMateriality(), or at the rating of the 8-K
// items, or at 2 for a quarterly report (its results came in an earlier 8-K
// item 2.02). It goes up by 1 when the
// text holds a magnitude phrase (record, plunge, surge, going concern,
// material weakness, significantly) and down by 1 when the kind is not noise
// and the text is tagged routine (in line with, as expected, regular quarterly
// dividend). It is then clamped to 1..5. Noise kinds are analyst_target and
// price_chatter.
//
// 3. Relevance = clamp01(0.20 + 0.15*(materiality-1)) + itemBonus +
// keywordAdjust + tierAdjust, clamped to [0,1] and rounded to 3 decimals. keywordAdjust is
// +0.06 per distinct Boost phrase found and -0.06 per distinct Demote phrase
// found, with the total capped to [-0.20, +0.20]. Phrases come from the
// configuration; matching ignores case, respects word boundaries and treats a
// multi-word phrase as one phrase. A few phrases also match listed aliases
// (the Boost word "buyback" also matches "share repurchase"), see
// phraseAliases. itemBonus is +0.02 for each other 8-K item of another kind, up
// to three (an earnings release filed with a presentation says more than one
// filed alone). A materiality of 3 gives 0.50, the default threshold, so an
// event of "new information on an expected path" counts unless the words demote
// it. tierAdjust is +0.05 when Options.SourceTier reports
// TierPrimary, -0.05 for TierCommentary and 0 for anything else or when the
// hook is nil. When Title and Summary are both empty the relevance is 0 and the
// reasons say "no text". Noise kinds cap the relevance at 0.35.
//
// 4. Tone and Sector are left at their zero values: a rule table cannot judge
// either.
//
// 5. Reasons lists every rule that fired, in the order the steps above ran, so
// a verdict can be traced to its words.
//
// Version changes whenever the Boost or Demote words, the pattern tables or the
// numeric weights change. The two hooks are the caller's and are not part of
// it.
package lens

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/config"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Weights of the formula in the package comment.
const (
	baseRelevance = 0.20
	perLevel      = 0.15
	perKeyword    = 0.06
	keywordCap    = 0.20
	tierStep      = 0.05
	noiseCap      = 0.35
)

// Options are the hooks a caller wires in. Both are optional.
type Options struct {
	// SourceTier says how much the source with that ID is trusted. The caller
	// wires the source registry.
	SourceTier func(sourceID string) model.Tier
	// ItemKind maps the 8-K item numbers of one filing to an event kind and
	// reports whether it knows them. The caller wires EDGAR's item table.
	ItemKind func(items []string) (model.EventKind, bool)
	// ItemRating reads the 8-K items of one filing into a kind, a materiality
	// and a bonus for the other items; it is tried before ItemKind. The caller
	// wires EDGAR's item table.
	ItemRating func(items []string) (model.ItemRating, bool)
}

// Lens scores articles against the configured rules. It is safe for
// concurrent use.
type Lens struct {
	boost   []string // normalised, distinct, sorted
	demote  []string
	opts    Options
	version string
}

// New returns a Lens using cfg and opts.
func New(cfg config.Lens, opts Options) *Lens {
	l := &Lens{boost: phrases(cfg.Boost), demote: phrases(cfg.Demote), opts: opts}
	l.version = "l0-1-" + digest(l.boost, l.demote)
	return l
}

// Version identifies the rules and words this Lens scores with. It is "l0-1-"
// followed by 8 hex characters of a hash over the sorted Boost and Demote
// words and the rule tables.
func (l *Lens) Version() string { return l.version }

// Score rates one article and records which rules fired.
func (l *Lens) Score(a model.Article) model.Score {
	title := normalize(a.Title)
	text := normalize(a.Title + "\n" + a.Summary)

	kind, why, fixed, bonus := l.classify(a, title, text)
	reasons := []string{why, "kind " + string(kind)}

	mat := kind.BaseMateriality()
	if fixed > 0 {
		mat = fixed
		reasons = append(reasons, fmt.Sprintf("materiality %d from the form", mat))
	} else {
		reasons = append(reasons, fmt.Sprintf("base materiality %d", mat))
	}
	noise := noiseKinds[kind]
	if m := firstMagnitude(text); m != "" {
		mat++
		reasons = append(reasons, fmt.Sprintf("magnitude phrase %q: materiality +1", m))
	}
	if !noise {
		if r := firstMatch(routineRules, text); r != "" {
			mat--
			reasons = append(reasons, fmt.Sprintf("routine phrase %q: materiality -1", r))
		}
	}
	if clamped := min(max(mat, 1), 5); clamped != mat {
		reasons = append(reasons, fmt.Sprintf("materiality clamped to %d", clamped))
		mat = clamped
	}

	score := model.Score{Kind: kind, Materiality: mat, LensVersion: l.version}
	if strings.TrimSpace(a.Title) == "" && strings.TrimSpace(a.Summary) == "" {
		score.Reasons = append(reasons, "no text")
		return score
	}

	rel := clamp01(baseRelevance + perLevel*float64(mat-1))
	if bonus > 0 {
		rel += bonus
		reasons = append(reasons, fmt.Sprintf("other 8-K items: relevance +%.2f", bonus))
	}
	kw, kwReasons := l.keywordAdjust(text)
	rel += kw
	reasons = append(reasons, kwReasons...)
	if l.opts.SourceTier != nil {
		switch l.opts.SourceTier(a.SourceID) {
		case model.TierPrimary:
			rel += tierStep
			reasons = append(reasons, "source tier primary: relevance +0.05")
		case model.TierCommentary:
			rel -= tierStep
			reasons = append(reasons, "source tier commentary: relevance -0.05")
		}
	}
	rel = clamp01(rel)
	if noise && rel > noiseCap {
		rel = noiseCap
		reasons = append(reasons, fmt.Sprintf("noise kind %s: relevance capped at %.2f", kind, noiseCap))
	}
	score.Relevance = math.Round(rel*1000) / 1000
	score.Reasons = reasons
	return score
}

// classify picks the event kind and says why. fixed is a materiality the
// filing itself decides (0 when the kind's base is used) and bonus the relevance
// that other 8-K items add.
func (l *Lens) classify(a model.Article, title, text string) (kind model.EventKind, why string, fixed int, bonus float64) {
	if len(a.Items) > 0 && l.opts.ItemRating != nil {
		if r, ok := l.opts.ItemRating(a.Items); ok && r.Kind.Valid() && r.Materiality >= 1 && r.Materiality <= 5 {
			why = "8-K item " + r.Item
			if name, _, known := model.ItemTitle(r.Item); known {
				why += " (" + name + ")"
			}
			if len(r.Also) > 0 {
				why += ", also " + strings.Join(r.Also, ", ")
			}
			return r.Kind, why, r.Materiality, r.Bonus
		}
	}
	k, w := l.classifyKind(a, title, text)
	if f := strings.ToUpper(strings.TrimSpace(a.Form)); strings.HasPrefix(f, "10-Q") && k == model.EventResults {
		return k, w + " (the results were reported in an 8-K item 2.02 before)", 2, 0
	}
	return k, w, 0, 0
}

func (l *Lens) classifyKind(a model.Article, title, text string) (model.EventKind, string) {
	if len(a.Items) > 0 && l.opts.ItemKind != nil {
		if k, ok := l.opts.ItemKind(a.Items); ok && k.Valid() {
			return k, "8-K item " + strings.Join(a.Items, ", ")
		}
	}
	form := strings.ToUpper(strings.TrimSpace(a.Form))
	switch {
	case form == "4" || form == "4/A":
		if w := formBuy.FindString(title); w != "" {
			return model.EventInsiderBuy, fmt.Sprintf("form 4 title word %q", w)
		}
		if w := formSell.FindString(title); w != "" {
			return model.EventInsiderSell, fmt.Sprintf("form 4 title word %q", w)
		}
		return model.EventOther, "form 4 title has no buy or sell word"
	case strings.HasPrefix(form, "10-K") || strings.HasPrefix(form, "10-Q"):
		return model.EventResults, "form " + form
	case form13DG.MatchString(form):
		return model.Event13DG, "form " + form
	}
	for _, r := range kindRules {
		if m := r.match(text); m != "" {
			return r.kind, fmt.Sprintf("keyword %q", m)
		}
	}
	return model.EventOther, "no kind rule matched"
}

// keywordAdjust is the capped Boost and Demote adjustment, with its reasons.
func (l *Lens) keywordAdjust(text string) (float64, []string) {
	var reasons []string
	n := 0
	for _, p := range l.boost {
		if phraseFound(text, p) {
			n++
			reasons = append(reasons, fmt.Sprintf("boost %q: relevance +0.06", p))
		}
	}
	for _, p := range l.demote {
		if phraseFound(text, p) {
			n--
			reasons = append(reasons, fmt.Sprintf("demote %q: relevance -0.06", p))
		}
	}
	adj := perKeyword * float64(n)
	switch {
	case adj > keywordCap:
		adj = keywordCap
		reasons = append(reasons, "keyword adjustment capped at +0.20")
	case adj < -keywordCap:
		adj = -keywordCap
		reasons = append(reasons, "keyword adjustment capped at -0.20")
	}
	return adj, reasons
}

// kindRule assigns a kind to text in which every pattern matches.
type kindRule struct {
	kind model.EventKind
	all  []*regexp.Regexp
}

// match returns the text the first pattern matched, or "" if some pattern
// does not match.
func (r kindRule) match(text string) string {
	first := ""
	for i, re := range r.all {
		m := re.FindString(text)
		if m == "" {
			return ""
		}
		if i == 0 {
			first = m
		}
	}
	return first
}

func rule(kind model.EventKind, patterns ...string) kindRule {
	r := kindRule{kind: kind}
	for _, p := range patterns {
		r.all = append(r.all, regexp.MustCompile(p))
	}
	return r
}

// kindRules is the ordered keyword table; the first rule that matches wins.
// Text is lower-cased before matching. Specific corporate events come first,
// then analyst notes (so "price target ahead of earnings" is an analyst note,
// not results), then reporting and people, and price chatter last because
// almost any story can mention a share price move.
var kindRules = []kindRule{
	rule(model.EventBuyback, `\bbuy[- ]?backs?\b`),
	rule(model.EventBuyback, `\brepurchas(?:e|es|ed|ing)\b`),
	rule(model.EventDividend, `\bdividends?\b`),
	rule(model.EventBankruptcy, `\bchapter 11\b`),
	rule(model.EventBankruptcy, `\bbankrupt(?:cy|cies)?\b`),
	rule(model.EventNonReliance, `\brestat(?:e|es|ed|ing|ement|ements)\b`),
	rule(model.EventNonReliance, `\bnon-?reliance\b`),
	rule(model.EventCyber, `\bcyber\w*`, `\b(?:incidents?|attacks?|breach\w*|ransomware)\b`),
	rule(model.EventDelisting, `\bdelist(?:s|ed|ing|ings)?\b`),
	rule(model.EventSpinoff, `\bspin[- ]?offs?\b`),
	rule(model.EventAcquisition, `\bacqui(?:re|res|red|ring|sition|sitions)\b`),
	rule(model.EventAcquisition, `\bmergers?\b`),
	rule(model.EventAcquisition, `\btakeovers?\b`),
	rule(model.EventAnalystTgt, `\bprice targets?\b`),
	rule(model.EventAnalystTgt, `\b(?:upgrad(?:e|es|ed|ing)|downgrad(?:e|es|ed|ing))\b`),
	rule(model.EventAnalystTgt, `\binitiat(?:e|es|ed|ing) coverage\b`),
	rule(model.EventGuidance, `\bguidance\b`),
	rule(model.EventGuidance, `\boutlook\b`),
	rule(model.EventGuidance, `\bforecasts?\b`),
	rule(model.EventResults, `\bquarterly results\b`),
	rule(model.EventResults, `\bearnings\b`),
	rule(model.EventResults, `\bresults for the\b`),
	rule(model.EventResults, `\bfiscal year\b`),
	rule(model.EventOfficerChange,
		`\b(?:chief executive|ceo|cfo|chief financial officer)\b`,
		`\b(?:appoint\w*|resign\w*|step(?:s|ped|ping)? down|retire\w*)\b`),
	rule(model.EventInsiderBuy,
		`\b(?:insiders?|form 4)\b`,
		`\b(?:buys?|bought|buying|purchas\w*)\b`),
	rule(model.EventInsiderSell,
		`\b(?:insiders?|form 4)\b`,
		`\b(?:sells?|sold|selling|sales?)\b`),
	rule(model.Event13DG, `\b13[dg]\b`),
	rule(model.EventLitigation, `\b(?:lawsuits?|litigation)\b`),
	rule(model.EventLitigation, `\bsettlements?\b`),
	rule(model.EventRates, `\brate decision\b`),
	rule(model.EventRates, `\bfederal funds\b`),
	rule(model.EventRates, `\bfomc\b`),
	rule(model.EventInflation, `\bcpi\b`),
	rule(model.EventInflation, `\binflation\b`),
	rule(model.EventPriceChatter,
		`\b(?:shares|stock)\b(?:\s+\S+){0,4}?\s+(?:rose|rise|rises|fell|fall|falls|jump\w*|surg\w*|plung\w*|slump\w*)\b`),
}

// Form-based rules. Form 4 is read from the title only.
var (
	formBuy   = regexp.MustCompile(`\b(?:buys?|bought|buying|purchas\w*)\b`)
	formSell  = regexp.MustCompile(`\b(?:sells?|sold|selling|sales?)\b`)
	form13DG  = regexp.MustCompile(`13[DG]`)
	formsRule = "4,4/A:title buy|sell; 10-K*,10-Q*:results; 13D,13G:13d_13g"
)

// magnitudeRules are phrases that make an event bigger than its kind alone.
var magnitudeRules = []*regexp.Regexp{
	regexp.MustCompile(`\brecord\b`),
	regexp.MustCompile(`\bplung(?:e|es|ed|ing)\b`),
	regexp.MustCompile(`\bsurg(?:e|es|ed|ing)\b`),
	regexp.MustCompile(`\bgoing concern\b`),
	regexp.MustCompile(`\bmaterial weakness(?:es)?\b`),
	regexp.MustCompile(`\bsignificantly\b`),
}

// routineRules are phrases that tag an event as expected and unremarkable.
var routineRules = []*regexp.Regexp{
	regexp.MustCompile(`\bin line with\b`),
	regexp.MustCompile(`\bas expected\b`),
	regexp.MustCompile(`\bregular quarterly dividend\b`),
}

// noiseKinds are the kinds a long-term investor ignores; their relevance is
// capped and they are never marked routine.
var noiseKinds = map[model.EventKind]bool{
	model.EventAnalystTgt:   true,
	model.EventPriceChatter: true,
}

// phraseAliases lists other wordings that count as a configured word. A
// configured word that has no entry matches only itself.
var phraseAliases = map[string][]string{
	"buyback":        {"buybacks", "buy back", "buy-back", "repurchase", "repurchases", "share repurchase"},
	"dividend":       {"dividends"},
	"insider buying": {"insider purchase", "insider purchases"},
	"price target":   {"price targets"},
	"spin-off":       {"spinoff", "spin off", "spin-offs", "spinoffs"},
}

// firstMagnitude returns the first magnitude phrase in text, or "". A record
// date is a dividend term, not a record result, and is ignored.
func firstMagnitude(text string) string {
	return firstMatch(magnitudeRules, strings.ReplaceAll(text, "record date", " "))
}

func firstMatch(res []*regexp.Regexp, text string) string {
	for _, re := range res {
		if m := re.FindString(text); m != "" {
			return m
		}
	}
	return ""
}

// phraseFound reports whether text holds phrase or one of its aliases as whole
// words.
func phraseFound(text, phrase string) bool {
	if hasPhrase(text, phrase) {
		return true
	}
	for _, alt := range phraseAliases[phrase] {
		if hasPhrase(text, alt) {
			return true
		}
	}
	return false
}

// hasPhrase reports whether phrase occurs in text with a word boundary at each
// end where the phrase itself starts or ends with a letter or digit.
func hasPhrase(text, phrase string) bool {
	if phrase == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(phrase)
	last, _ := utf8.DecodeLastRuneInString(phrase)
	for from := 0; from+len(phrase) <= len(text); {
		i := strings.Index(text[from:], phrase)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(phrase)
		okBefore, okAfter := true, true
		if isWord(first) && i > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			okBefore = !isWord(r)
		}
		if isWord(last) && end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			okAfter = !isWord(r)
		}
		if okBefore && okAfter {
			return true
		}
		from = i + 1
	}
	return false
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

var punctuation = strings.NewReplacer(
	"‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-",
	"‘", "'", "’", "'",
)

// normalize lower-cases s, unifies dashes and quotes and collapses white space.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(punctuation.Replace(s))), " ")
}

// phrases returns the normalised, distinct, non-empty words, sorted.
func phrases(words []string) []string {
	seen := make(map[string]bool, len(words))
	var out []string
	for _, w := range words {
		w = normalize(w)
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

func clamp01(x float64) float64 { return math.Min(math.Max(x, 0), 1) }

// rulesEncoding is the canonical text of everything in the formula except the
// configured words.
var rulesEncoding = encodeRules()

func encodeRules() string {
	var b strings.Builder
	fmt.Fprintf(&b, "weights %v %v %v %v %v %v\n", baseRelevance, perLevel, perKeyword, keywordCap, tierStep, noiseCap)
	for _, r := range kindRules {
		fmt.Fprintf(&b, "kind %q", string(r.kind))
		for _, re := range r.all {
			fmt.Fprintf(&b, " %q", re.String())
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "form %q %q %q %q\n", formsRule, formBuy.String(), formSell.String(), form13DG.String())
	for _, re := range magnitudeRules {
		fmt.Fprintf(&b, "magnitude %q\n", re.String())
	}
	for _, re := range routineRules {
		fmt.Fprintf(&b, "routine %q\n", re.String())
	}
	var noise []string
	for k := range noiseKinds {
		noise = append(noise, string(k))
	}
	sort.Strings(noise)
	fmt.Fprintf(&b, "noise %q\n", noise)
	keys := make([]string, 0, len(phraseAliases))
	for k := range phraseAliases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		alts := append([]string(nil), phraseAliases[k]...)
		sort.Strings(alts)
		fmt.Fprintf(&b, "alias %q %q\n", k, alts)
	}
	return b.String()
}

// digest is the first 8 hex characters of sha256 over the rules and words.
func digest(boost, demote []string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%sboost %q\ndemote %q\n", rulesEncoding, boost, demote)))
	return hex.EncodeToString(sum[:])[:8]
}
