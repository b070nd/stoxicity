package scorecard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Versions are the versions of everything that turns inputs into a scorecard.
// They are part of every content hash and of every plan manifest, so a change
// to the extractor, the rules or the keyword table is visible as a change.
type Versions struct {
	Extractor string `json:"extractor"`
	Rules     string `json:"rules"`
	Keywords  string `json:"keywords"`
}

// ExtractorVersion changes when the way figures are selected and ratios are
// computed changes.
const ExtractorVersion = "scorecard-extract/1"

// CurrentVersions are the versions in this build.
func CurrentVersions() Versions {
	return Versions{Extractor: ExtractorVersion, Rules: "scorecard-rules/1:" + rulesDigest(), Keywords: "scorecard-keywords/1:" + KeywordDigest()}
}

type keyword struct {
	Flag  string
	Terms []string
}

// keywordTable tags qualitative notes with the risk themes they mention. The
// match is a plain, case-insensitive phrase search: a flag says that a phrase
// is present, not that the risk is real.
var keywordTable = []keyword{
	{"going_concern", []string{"going concern"}},
	{"restatement", []string{"restatement", "restated"}},
	{"material_weakness", []string{"material weakness"}},
	{"litigation", []string{"lawsuit", "litigation", "class action"}},
	{"dilution", []string{"dilution", "share issuance", "convertible"}},
	{"refinancing", []string{"covenant", "refinanc"}},
	{"dividend_cut", []string{"dividend cut", "suspended dividend", "dividend suspension"}},
	{"customer_concentration", []string{"customer concentration", "single customer"}},
}

// KeywordDigest identifies the keyword table.
func KeywordDigest() string {
	var b strings.Builder
	for _, k := range keywordTable {
		fmt.Fprintf(&b, "%s=%s\n", k.Flag, strings.Join(k.Terms, "|"))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// Flags lists the themes a text mentions, sorted.
func Flags(text string) []string {
	low := strings.ToLower(text)
	var out []string
	for _, k := range keywordTable {
		for _, t := range k.Terms {
			if strings.Contains(low, t) {
				out = append(out, k.Flag)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// Stance of an annotation toward owning the security.
type Stance string

const (
	Supports Stance = "supports"
	Counter  Stance = "counter"
	Neutral  Stance = "neutral"
)

// Topics an annotation can be about.
var Topics = []string{"moat", "management", "balance_sheet", "income", "valuation", "risk", "thesis", "other"}

// Annotation is a qualitative note with its origin. It never changes a number;
// it is shown beside them and read by the strategy as thesis or counter-evidence.
type Annotation struct {
	Security string    `json:"security"`
	Topic    string    `json:"topic"`
	Stance   Stance    `json:"stance"`
	Text     string    `json:"text"`
	Source   string    `json:"source"`
	Ref      string    `json:"ref"`
	At       time.Time `json:"at"`
	Flags    []string  `json:"flags,omitempty"`
}

// Validate says why the note is not acceptable.
func (a Annotation) Validate() error {
	ok := false
	for _, t := range Topics {
		ok = ok || t == a.Topic
	}
	switch {
	case !securityRE.MatchString(a.Security):
		return fmt.Errorf("scorecard: the security %q must be an identifier", a.Security)
	case !ok:
		return fmt.Errorf("scorecard: unknown topic %q (%s)", a.Topic, strings.Join(Topics, ", "))
	case a.Stance != Supports && a.Stance != Counter && a.Stance != Neutral:
		return fmt.Errorf("scorecard: unknown stance %q (supports, counter, neutral)", a.Stance)
	case strings.TrimSpace(a.Text) == "":
		return fmt.Errorf("scorecard: a note needs text")
	case strings.TrimSpace(a.Source) == "" || strings.TrimSpace(a.Ref) == "":
		return fmt.Errorf("scorecard: a note needs its source and reference: an opinion with no origin is not kept")
	}
	return nil
}

func (a Annotation) canonical() string {
	return strings.Join([]string{a.Security, a.Topic, string(a.Stance), a.Text, a.Source, a.Ref, a.At.UTC().Format(time.RFC3339)}, "\x1f")
}
