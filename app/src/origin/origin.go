// Package origin decides where the reporting in an article came from, apart
// from who published it.
//
// A publisher is a site. A reporting origin is whoever first established what
// the article says: the company in a filing or a release, a news agency's
// reporter, a newsroom's own reporter. Ten sites carrying one wire story are ten
// publishers and one origin, and an article that quotes a filing is a publisher
// of a derivative, not a second confirmation of the filing.
//
// Detection is rule based and conservative, in this order of strength:
//
//   - a filing, or a document of a primary source (the accession is the origin);
//   - a release distributor's tag (Business Wire, PR Newswire, GlobeNewswire):
//     the company speaking for itself;
//   - a news agency's attribution (dateline, "Reporting by", credit line);
//   - an explicit provider story ID, when the feed supplies one;
//   - a reference to a filing's accession number or an attribution to a filing
//     or a press release: the article is derived from that document;
//   - a byline of the publisher's own, which is weak evidence of its own reporting;
//   - otherwise the origin is unknown, and stays unknown.
//
// Link then looks at the articles about one story together and joins exact and
// near copies to the first article they copy. Topic or event similarity is not
// used for any of this: two articles about the same acquisition are not copies
// of each other unless their text is.
//
// Everything is a pure function; the same input gives the same answer, and every
// answer states the method, a confidence and the text that was matched.
package origin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Version names the rules. It is stored with every origin so that a later
// version adds rows instead of rewriting what an earlier one found.
const Version = "origin-2"

// Kind says what sort of origin it is.
type Kind string

const (
	KindPrimary  Kind = "primary"  // a filing or a document of a primary source
	KindIssuer   Kind = "issuer"   // a company's own release through a distributor
	KindWire     Kind = "wire"     // a news agency's story
	KindReported Kind = "reported" // a publisher's own byline, nothing seen that it is derived
	KindDerived  Kind = "derived"  // refers to a document, nothing of its own seen
	KindUnknown  Kind = "unknown"  // nothing in the article says where it came from
)

// Method says how the origin was found.
type Method string

const (
	MethodFilingAccession Method = "filing-accession"
	MethodSourceTier      Method = "source-tier"
	MethodReleaseTag      Method = "release-distributor"
	MethodWireAttribution Method = "wire-attribution"
	MethodProviderID      Method = "provider-id"
	MethodCanonicalRef    Method = "canonical-reference"
	MethodAttribution     Method = "attribution-phrase"
	MethodByline          Method = "byline"
	MethodExactCopy       Method = "exact-copy"
	MethodNearCopy        Method = "near-copy"
	MethodPassageOverlap  Method = "passage-overlap"
	MethodNone            Method = "none"
)

// Input is what detection reads of one article.
type Input struct {
	ArticleID  string
	SourceID   string
	Domain     string
	URL        string
	Title      string
	Summary    string
	Form       string // SEC form, empty when the article is not a filing
	Primary    bool   // the source is a primary one (tier 1)
	ProviderID string // the provider's own story ID, when the feed gives one
	Published  time.Time
}

// Origin is the reporting origin of one article.
type Origin struct {
	ArticleID string
	Publisher string // the site that carried it
	Kind      Kind
	// Key is the identity of the origin: articles with the same Key are copies
	// of one report and count once.
	Key string
	// DerivedFrom is the Key of a document the report rests on (a filing, a
	// release), or empty when none was seen. A report that rests on a document
	// does not confirm it.
	DerivedFrom string
	// CopyOf is the article this one copies, for exact and near copies.
	CopyOf string
	// SuspectedFrom is the article this one is suspected to derive from: a
	// substantial passage of its own words equals a passage of that article, but
	// not enough to call it a copy. It is a suspicion, not a finding of a common
	// origin, so the origin keeps its own Key; until the suspicion is settled it
	// is not counted as an independent report.
	SuspectedFrom string
	// Matches are the observations behind CopyOf and SuspectedFrom.
	Matches    []Match
	Method     Method
	Confidence float64 // 0..1, how sure the rule is, not how true the report is
	Detail     string  // the text or fact that decided it
	Version    string
}

// Match is what was seen when two articles were compared.
type Match struct {
	Other      string  // the article compared with
	Method     Method  // exact-copy, near-copy or passage-overlap
	Score      float64 // the measure the method uses (share of equal word runs)
	Shared     int     // three-word runs the two articles share
	LongestRun int     // the longest run of consecutive equal words
	Passage    string  // the longest shared run, as written in the earlier article
}

// Standing says how far an origin is established as a report of its own. It is
// what the corroboration verdict reads, never the publisher's domain: ten sites
// are ten publishers however they are named.
type Standing string

const (
	// StandingOwn: a primary document or the issuer's own release.
	StandingOwn Standing = "own"
	// StandingAttributed: the text names a news agency as its source (a
	// dateline, a credit line). The attribution is the text's own claim and is not
	// verified, but it is explicit and it names who reported.
	StandingAttributed Standing = "attributed"
	// StandingClaimed: the article claims reporting of its own (a byline, a
	// provider's story ID) and nothing more. Rewrites of other reports carry
	// bylines too, so this does not establish independence.
	StandingClaimed Standing = "claimed"
	// StandingSuspected: a substantial passage equals one of an earlier article.
	StandingSuspected Standing = "suspected-derivative"
	// StandingDerived: rests on a document or another report.
	StandingDerived Standing = "derived"
	// StandingUnknown: nothing says where it came from.
	StandingUnknown Standing = "unknown"
)

// Standing classifies the origin.
func (o Origin) Standing() Standing {
	switch {
	case o.Own():
		return StandingOwn
	case o.Kind == KindUnknown:
		return StandingUnknown
	case o.Kind == KindDerived || o.DerivedFrom != "":
		return StandingDerived
	case o.SuspectedFrom != "":
		return StandingSuspected
	case o.Kind == KindWire:
		return StandingAttributed
	default:
		return StandingClaimed
	}
}

// Independent says whether the origin can stand as a confirmation apart from
// the issuer's own statements: an agency's attributed report that rests on no
// document and is not suspected of being derived from another article. A byline
// alone, a distinct domain, or an unknown origin never is.
func (o Origin) Independent() bool { return o.Standing() == StandingAttributed }

// Own says whether the origin is the issuer or a primary document speaking for
// itself.
func (o Origin) Own() bool { return o.Kind == KindPrimary || o.Kind == KindIssuer }

var (
	// agencies are the news agencies whose attribution is recognised, with the
	// names they appear under.
	agencies = []struct {
		name string
		alts []string
	}{
		{"reuters", []string{"Reuters"}},
		{"ap", []string{"Associated Press", "AP"}},
		{"afp", []string{"Agence France-Presse", "AFP"}},
		{"bloomberg", []string{"Bloomberg News", "Bloomberg"}},
		{"dow-jones", []string{"Dow Jones Newswires", "Dow Jones"}},
		{"rttnews", []string{"RTTNews"}},
		{"mt-newswires", []string{"MT Newswires"}},
		{"kyodo", []string{"Kyodo"}},
		{"xinhua", []string{"Xinhua"}},
	}
	agencyDateline = compileAgencies(`\(\s*(?:%s)\s*\)\s*[-–—:]`)
	agencyReport   = compileAgenciesFold(`\b(?:reporting|reported|written|story|additional reporting|editing)\s+by\b[^.\n]{0,100}?\b(?:%s)\b`)
	agencyCredit   = compileAgenciesFold(`(?:\b(?:source|credit|via)\s*:|[-–—]\s*)\s*(?:%s)\s*$`)

	distributors = []struct{ name, pattern string }{
		{"business-wire", `BUSINESS WIRE|Business Wire`},
		{"prnewswire", `PR ?Newswire|PRNewswire`},
		{"globenewswire", `GLOBE ?NEWSWIRE|GlobeNewswire`},
		{"accesswire", `ACCESSWIRE|Accesswire`},
	}
	distributorDomains = map[string]string{
		"businesswire.com": "business-wire", "prnewswire.com": "prnewswire", "globenewswire.com": "globenewswire", "accesswire.com": "accesswire",
	}

	accession  = regexp.MustCompile(`\b(\d{10}-\d{2}-\d{6})\b`)
	secArchive = regexp.MustCompile(`(?i)sec\.gov/Archives/edgar/data/`)
	phrase     = regexp.MustCompile(`(?i)\b(?:according to|said in|stated in|announced in|disclosed in|detailed in|showed in)\s+(?:a|an|its|the|their)\s+(?:company\s+|regulatory\s+|sec\s+)?(?:press release|news release|statement|filing|earnings release|8-k|10-k|10-q)\b|\b(?:filing|filed) with the (?:u\.s\. )?(?:securities and exchange commission|sec)\b|\bsec filing\b`)
	byline     = regexp.MustCompile(`(?m)(?:^|\n)\s*By\s+[A-Z][\p{L}.'-]+(?:\s+[A-Z][\p{L}.'-]+){1,3}\b`)
)

// agencyAlternatives is the regular expression alternation of every agency name.
func agencyAlternatives() string {
	var alts []string
	for _, a := range agencies {
		for _, n := range a.alts {
			alts = append(alts, regexp.QuoteMeta(n))
		}
	}
	return strings.Join(alts, "|")
}

func compileAgencies(format string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(format, agencyAlternatives()))
}

func compileAgenciesFold(format string) *regexp.Regexp {
	return regexp.MustCompile("(?i)" + fmt.Sprintf(format, agencyAlternatives()))
}

// agencyOf names the agency whose name occurs in the matched text.
func agencyOf(matched string) string {
	low := strings.ToLower(matched)
	for _, a := range agencies {
		for _, n := range a.alts {
			if strings.Contains(low, strings.ToLower(n)) {
				return a.name
			}
		}
	}
	return ""
}

// head is the part of the article that attribution lives in: the title and the
// start of the text.
func head(in Input) string {
	s := in.Summary
	if len(s) > 600 {
		s = s[:600]
	}
	return in.Title + "\n" + s
}

// Detect finds the origin of one article by its own text alone.
func Detect(in Input) Origin {
	o := Origin{ArticleID: in.ArticleID, Publisher: in.Domain, Version: Version, Method: MethodNone}
	text := head(in)

	if in.Form != "" || in.Primary {
		o.Kind, o.Key, o.Confidence = KindPrimary, "primary:"+in.ArticleID, 1
		if in.Form != "" {
			o.Method, o.Detail = MethodFilingAccession, "SEC form "+in.Form+", accession "+in.ArticleID
		} else {
			o.Method, o.Detail = MethodSourceTier, "source "+in.SourceID+" is a primary source"
		}
		return o
	}
	if name, ok := distributorDomains[strings.TrimPrefix(in.Domain, "www.")]; ok {
		o.Kind, o.Key, o.Method, o.Confidence, o.Detail = KindIssuer, "issuer:"+name, MethodReleaseTag, 0.9, "published on "+in.Domain
		return o
	}
	for _, d := range distributors {
		re := regexp.MustCompile(`(?:\(|/|\b)(?:` + d.pattern + `)(?:\)|/|\b)`)
		if loc := re.FindStringIndex(text); loc != nil && loc[0] < 400 {
			o.Kind, o.Key, o.Method, o.Confidence, o.Detail = KindIssuer, "issuer:"+d.name, MethodReleaseTag, 0.9, strings.TrimSpace(text[loc[0]:loc[1]])
			return o
		}
	}

	var (
		agency, why string
		conf        float64
	)
	if m := agencyDateline.FindString(text); m != "" {
		agency, why, conf = agencyOf(m), m, 0.9
	} else if m := agencyReport.FindString(text); m != "" {
		agency, why, conf = agencyOf(m), m, 0.7
	} else if m := agencyCredit.FindString(strings.TrimSpace(text)); m != "" {
		agency, why, conf = agencyOf(m), m, 0.7
	}
	derived, derivedWhy, derivedConf := derivation(in, text)
	switch {
	case agency != "":
		o.Kind, o.Key, o.Method, o.Confidence, o.Detail = KindWire, "wire:"+agency, MethodWireAttribution, conf, strings.TrimSpace(why)
		if in.ProviderID != "" {
			o.Detail += "; provider story " + in.ProviderID
		}
	case in.ProviderID != "":
		o.Kind, o.Key, o.Method, o.Confidence, o.Detail = KindReported, "provider:"+in.ProviderID, MethodProviderID, 0.7, "provider story "+in.ProviderID
	case derived != "":
		o.Kind, o.Key, o.Method, o.Confidence, o.Detail = KindDerived, "derived:"+derived, MethodCanonicalRef, derivedConf, derivedWhy
		o.DerivedFrom = derived
		return o
	case byline.MatchString(text):
		o.Kind, o.Key, o.Method, o.Confidence, o.Detail = KindReported, "reported:"+in.Domain, MethodByline, 0.5, strings.TrimSpace(byline.FindString(text))
	default:
		o.Kind, o.Key, o.Method, o.Confidence, o.Detail = KindUnknown, "unknown:"+in.Domain, MethodNone, 0, "nothing in the article names where it came from"
		return o
	}
	if derived != "" { // the report rests on a document: it can be read, but it does not confirm it
		o.DerivedFrom = derived
		o.Detail += "; rests on " + derivedWhy
	}
	return o
}

// derivation says which document the text rests on: a filing named by its
// accession, or an unnamed statement or filing the text attributes to.
func derivation(in Input, text string) (key, why string, conf float64) {
	hay := in.URL + "\n" + text
	if m := accession.FindString(hay); m != "" && m != in.ArticleID {
		return "primary:" + m, "filing " + m, 0.9
	}
	if secArchive.MatchString(hay) {
		return "primary:sec-archive", "a link to the SEC's archive", 0.8
	}
	if m := phrase.FindString(text); m != "" {
		return "issuer:statement", "\"" + strings.ToLower(m) + "\"", 0.6
	}
	return "", "", 0
}

// Fingerprint is a hash of the words of the title and the start of the text,
// without case, punctuation or markup: equal for exact copies and for copies
// that differ only in layout. It is empty for a text too short to compare.
func Fingerprint(title, summary string) string {
	w := words(title + " " + clip(summary, 600))
	if len(w) < 8 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(w, " ")))
	return hex.EncodeToString(sum[:8])
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// shingles are the sets of three consecutive words of the text.
func shingles(s string) map[string]bool {
	w := words(s)
	out := map[string]bool{}
	for i := 0; i+3 <= len(w); i++ {
		out[strings.Join(w[i:i+3], " ")] = true
	}
	return out
}

// NearCopy is the least share of shared three-word runs (of all the runs of
// both texts) at which two texts are taken for copies of one another.
const NearCopy = 0.85

// minNearWords is the fewest words a text needs to be compared for near copies:
// short headlines of different stories are alike by chance.
const minNearWords = 30

// Passage overlap, the weaker test: an edited syndication keeps long stretches
// of the original's words but adds, drops and reorders. Two texts are suspected
// of deriving one from the other when, with quoted words left out (outlets
// quote the same statement without copying each other), at least
// SuspectContainment of the shorter text's three-word runs occur in the other,
// or SuspectRun consecutive words are equal. Both texts need minSuspectWords
// words outside quotation marks.
const (
	SuspectContainment = 0.5
	SuspectRun         = 12
	minSuspectWords    = 20
)

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return float64(shared(a, b)) / float64(len(a)+len(b)-shared(a, b))
}

func shared(a, b map[string]bool) int {
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return inter
}

var quoted = regexp.MustCompile(`"[^"]{8,}"|\x{201C}[^\x{201D}]{8,}\x{201D}`)

// unquoted is the text without what is inside quotation marks.
func unquoted(s string) string { return quoted.ReplaceAllString(s, " ") }

// longestRun returns the longest run of consecutive equal words of a and b.
func longestRun(a, b []string) (n int, at int) {
	prev := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > n {
					n, at = cur[j], i-cur[j]
				}
			}
		}
		prev = cur
	}
	return n, at
}

// Link joins the copies among the articles of one story. An article whose text
// equals, or nearly equals, the text of an earlier article takes that article's
// origin. An article that shares a substantial passage with an earlier one, but
// is not a near copy, is marked as suspected of deriving from it and keeps its
// own origin. ins and os run in parallel; the result is a new slice in the same
// order. A primary document is never a copy of anything, and no finding rests on
// the topic: two articles about one acquisition are alike only if their words are.
func Link(ins []Input, os []Origin) []Origin {
	out := slices.Clone(os)
	order := make([]int, len(ins))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		if c := ins[a].Published.Compare(ins[b].Published); c != 0 {
			return c
		}
		return strings.Compare(ins[a].ArticleID, ins[b].ArticleID)
	})
	n := len(ins)
	fp := make([]string, n)
	sh, shq := make([]map[string]bool, n), make([]map[string]bool, n)
	nw, nq := make([]int, n), make([]int, n)
	wq := make([][]string, n)
	for i, in := range ins {
		text := in.Title + " " + clip(in.Summary, 600)
		fp[i] = Fingerprint(in.Title, in.Summary)
		sh[i] = shingles(text)
		nw[i] = len(words(text))
		wq[i] = words(unquoted(text))
		nq[i] = len(wq[i])
		shq[i] = shingles(unquoted(text))
	}
	for pos, i := range order {
		if out[i].Kind == KindPrimary {
			continue
		}
		var best *Match
		for _, j := range order[:pos] {
			if out[j].Kind == KindPrimary || ins[j].ArticleID == ins[i].ArticleID {
				continue
			}
			switch {
			case fp[i] != "" && fp[i] == fp[j]:
				m := Match{Other: ins[j].ArticleID, Method: MethodExactCopy, Score: 1, Shared: len(sh[i])}
				adopt(&out[i], out[j], m, 0.95, "the title and opening text equal those of "+ins[j].ArticleID)
				best = nil
			case nw[i] >= minNearWords && nw[j] >= minNearWords && jaccard(sh[i], sh[j]) >= NearCopy:
				sc := jaccard(sh[i], sh[j])
				m := Match{Other: ins[j].ArticleID, Method: MethodNearCopy, Score: sc, Shared: shared(sh[i], sh[j])}
				adopt(&out[i], out[j], m, 0.7, fmt.Sprintf("%.0f%% of the three-word runs equal those of %s", sc*100, ins[j].ArticleID))
				best = nil
			default:
				if nq[i] < minSuspectWords || nq[j] < minSuspectWords {
					continue
				}
				run, at := longestRun(wq[i], wq[j])
				short := min(len(shq[i]), len(shq[j]))
				contain := 0.0
				if short > 0 {
					contain = float64(shared(shq[i], shq[j])) / float64(short)
				}
				if contain < SuspectContainment && run < SuspectRun {
					continue
				}
				m := Match{Other: ins[j].ArticleID, Method: MethodPassageOverlap, Score: contain, Shared: shared(shq[i], shq[j]), LongestRun: run}
				if run > 0 {
					m.Passage = strings.Join(wq[i][at:at+run], " ")
				}
				if best == nil || m.Score > best.Score || (m.Score == best.Score && m.LongestRun > best.LongestRun) {
					best = &m
				}
				continue
			}
			break
		}
		if best != nil && out[i].CopyOf == "" {
			out[i].SuspectedFrom = best.Other
			out[i].Matches = append(slices.Clone(out[i].Matches), *best)
			out[i].Detail += fmt.Sprintf("; suspected derivative of %s (%.0f%% of its word runs recur, longest equal run %d words), not proven", best.Other, best.Score*100, best.LongestRun)
		}
	}
	return out
}

// adopt makes o a copy of src.
func adopt(o *Origin, src Origin, m Match, conf float64, why string) {
	o.Key, o.Kind, o.DerivedFrom, o.SuspectedFrom = src.Key, src.Kind, src.DerivedFrom, src.SuspectedFrom
	o.CopyOf, o.Method, o.Confidence, o.Detail = m.Other, m.Method, conf, why
	o.Matches = append(slices.Clone(o.Matches), m)
}
