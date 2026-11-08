// Package heldout measures how well the program reads news, against labels
// that someone independent wrote, on articles it was not tuned on.
//
// The evidence has three parts that are kept apart:
//
//   - a corpus of source-linked articles, each with its licence. Text is kept in
//     the corpus only when the licence lets it be redistributed; otherwise the
//     item holds a content hash and the text is read from the local article store,
//     and an item whose text no longer matches its hash is left out and reported;
//   - labels by named labellers, each attested independent or not. A label is
//     never edited: a revision is a new version that names the one it supersedes
//     and says why, and the history stays;
//   - disagreements. Two independent labellers who differ on a stage of an item
//     leave a disagreement record. It is settled only by an adjudication from a
//     third independent person with a rationale; until then the item is left out
//     of that stage's figures and counted as contested.
//
// Synthetic regression items are evaluated and reported in their own section.
// They never count toward the gate, and a missing external label cannot be a
// passed gate: with no independently labelled items the answer is "no external
// labels", and so it is with too few items, unfrozen or changed labels, or
// thresholds that were not stated before the run.
package heldout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/cluster"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Version names the evaluation rules.
const Version = "heldout-1"

// Stages of reading an article, in the order they are reported.
const (
	Extraction  = "extraction"  // which kinds of event the article claims
	Parties     = "parties"     // which company is the subject and which the counterparty
	Polarity    = "polarity"    // negated or denied, and the action
	Temporal    = "temporal"    // how far the event has come, and whether it is past
	Attribution = "attribution" // who vouches for the claim
	Abstention  = "abstention"  // an article that claims nothing about a company
	Identity    = "identity"    // which articles are the same event
)

// Stages lists them.
var Stages = []string{Extraction, Parties, Polarity, Temporal, Attribution, Abstention, Identity}

// Licence says what may be done with an article's text.
type Licence string

const (
	Redistributable Licence = "redistributable"
	Internal        Licence = "internal"
	Unknown         Licence = "unknown"
)

// Item is one article of the corpus.
type Item struct {
	ID        string   `json:"id"`
	ArticleID string   `json:"article_id,omitempty"` // in the local store, for text not kept here
	SourceID  string   `json:"source_id"`
	URL       string   `json:"url"`
	Licence   Licence  `json:"licence"`
	Terms     string   `json:"terms,omitempty"`
	Published string   `json:"published"` // YYYY-MM-DD or RFC 3339
	Language  string   `json:"language,omitempty"`
	Title     string   `json:"title,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Hash      string   `json:"content_hash"` // sha256 of title, a newline and the summary, as labelled
	Form      string   `json:"form,omitempty"`
	Filing    []string `json:"filing_items,omitempty"`
	Tickers   []string `json:"tickers,omitempty"`
	Synthetic bool     `json:"synthetic,omitempty"`
}

// ContentHash is the hash an item's text must have.
func ContentHash(title, summary string) string {
	h := sha256.Sum256([]byte(title + "\n" + summary))
	return hex.EncodeToString(h[:])
}

// Labeller is a person who labels.
type Labeller struct {
	ID string `json:"id"`
	// Independent is the attestation that this person did not write or tune the
	// extraction rules and had not seen the program's output on these items.
	Independent bool   `json:"independent"`
	Note        string `json:"note,omitempty"`
}

// EventDef names a real-world event the corpus's items may be about.
type EventDef struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// Corpus is the set of items and the vocabularies the labels use.
type Corpus struct {
	Name      string     `json:"name"`
	Items     []Item     `json:"items"`
	Labellers []Labeller `json:"labellers"`
	Events    []EventDef `json:"events,omitempty"`
	// Names gives the names a company is written by, so a sentence that names
	// the company without its symbol is tied to it.
	Names map[string][]string `json:"names,omitempty"`
}

// PartyLabel is a company in a claim.
type PartyLabel struct {
	Ticker string `json:"ticker"`
	Role   string `json:"role"` // subject or counterparty
}

// ClaimLabel is one claim an article makes.
type ClaimLabel struct {
	Kind        string       `json:"kind"`
	Parties     []PartyLabel `json:"parties,omitempty"`
	Status      string       `json:"status,omitempty"` // unknown, proposed, announced, completed
	Negated     bool         `json:"negated,omitempty"`
	Denied      bool         `json:"denied,omitempty"`
	Historical  bool         `json:"historical,omitempty"`
	Attribution string       `json:"attribution,omitempty"` // "", own, reported
	Action      string       `json:"action,omitempty"`
}

// ItemLabel is what a labeller says about an item.
type ItemLabel struct {
	Claims []ClaimLabel `json:"claims,omitempty"`
	// NoClaims says the article claims nothing about a listed company, so the
	// right reading is to abstain.
	NoClaims bool     `json:"no_claims,omitempty"`
	Event    string   `json:"event,omitempty"`  // a key of the corpus's events
	Slices   []string `json:"slices,omitempty"` // contradiction, correction, multilingual, paraphrase
}

// Record is one version of a labeller's label for an item. Versions only grow:
// a revision names the version it supersedes and gives the reason.
type Record struct {
	Labeller   string    `json:"labeller"`
	Item       string    `json:"item"`
	Version    int       `json:"version"`
	Supersedes int       `json:"supersedes,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Label      ItemLabel `json:"label"`
}

// Adjudication settles a disagreement on one stage of one item.
type Adjudication struct {
	Item      string   `json:"item"`
	Stage     string   `json:"stage"`
	By        string   `json:"by"`
	Tokens    []string `json:"tokens"`
	Rationale string   `json:"rationale"`
}

// Thresholds are stated before the evaluation and frozen with the labels.
type Thresholds struct {
	// MinAnnotators is how many independent labellers an item needs (at least 2).
	MinAnnotators int `json:"min_annotators"`
	// MinItems is how many gold items a stage needs before it can pass.
	MinItems int `json:"min_items"`
	// MinPrecision and MinRecall apply to the lower end of the 95% interval.
	MinPrecision float64 `json:"min_precision"`
	MinRecall    float64 `json:"min_recall"`
	// MinGroup is how many items a group needs to be worth reading.
	MinGroup int `json:"min_group"`
}

// Validate says why thresholds cannot be used.
func (t Thresholds) Validate() error {
	switch {
	case t.MinAnnotators < 2:
		return errors.New("heldout: at least two independent annotators per item are needed to see a disagreement")
	case t.MinItems < 1:
		return errors.New("heldout: min_items must be at least 1")
	case t.MinPrecision <= 0 || t.MinPrecision > 1 || t.MinRecall <= 0 || t.MinRecall > 1:
		return errors.New("heldout: min_precision and min_recall must be set above 0 and at most 1")
	case t.MinGroup < 1:
		return errors.New("heldout: min_group must be at least 1")
	}
	return nil
}

// ---- stage tokens ----

// Tokens are the facts a label states for a stage, sorted and unique.
func Tokens(l ItemLabel, stage string) []string {
	var out []string
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	for _, c := range l.Claims {
		switch stage {
		case Extraction:
			out = append(out, c.Kind)
		case Parties:
			for _, p := range c.Parties {
				if p.Role == "subject" || p.Role == "counterparty" {
					out = append(out, c.Kind+"|"+strings.ToUpper(p.Ticker)+"|"+p.Role)
				}
			}
		case Polarity:
			out = append(out, c.Kind+"|neg="+b(c.Negated)+"|den="+b(c.Denied)+"|act="+c.Action)
		case Temporal:
			status := c.Status
			if status == "" {
				status = "unknown"
			}
			out = append(out, c.Kind+"|"+status+"|hist="+b(c.Historical))
		case Attribution:
			out = append(out, c.Kind+"|"+c.Attribution)
		}
	}
	switch stage {
	case Abstention:
		if l.NoClaims && len(l.Claims) == 0 {
			out = append(out, "abstain")
		}
	case Identity:
		if l.Event != "" {
			out = append(out, l.Event)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ---- gold ----

// Disagreement is a stage of an item on which independent labellers differ.
type Disagreement struct {
	Item     string              `json:"item"`
	Stage    string              `json:"stage"`
	Values   map[string][]string `json:"values"` // by labeller
	Resolved bool                `json:"resolved"`
	By       string              `json:"resolved_by,omitempty"`
	Tokens   []string            `json:"resolved_tokens,omitempty"`
	Why      string              `json:"rationale,omitempty"`
	Problem  string              `json:"problem,omitempty"`
}

// Gold is the settled reading of the corpus.
type Gold struct {
	// Tokens[item][stage] is the settled value; absent when contested or unlabelled.
	Tokens        map[string]map[string][]string
	Labelled      map[string]bool // items with enough independent annotators
	Slices        map[string][]string
	Kinds         map[string][]string
	Disagreements []Disagreement
	// Agreement is, per stage, the items on which annotators agreed and all
	// items compared.
	Agreed, Compared map[string]int
	// Raw[item][stage] says the annotators agreed before any adjudication.
	Raw map[string]map[string]bool
	// Superseded counts label versions that a later one replaced.
	Superseded int
	Problems   []string
}

// Latest keeps, for each labeller and item, the highest version.
func Latest(recs []Record) (map[[2]string]Record, int) {
	out := map[[2]string]Record{}
	n := 0
	for _, r := range recs {
		k := [2]string{r.Labeller, r.Item}
		if cur, ok := out[k]; ok {
			n++
			if r.Version <= cur.Version {
				continue
			}
		}
		out[k] = r
	}
	return out, n
}

// ValidateRecords checks the history of versions: a new version must name the
// one it replaces and give a reason; the first has neither.
func ValidateRecords(recs []Record) error {
	by := map[[2]string][]Record{}
	for _, r := range recs {
		k := [2]string{r.Labeller, r.Item}
		by[k] = append(by[k], r)
	}
	for k, rs := range by {
		slices.SortFunc(rs, func(a, b Record) int { return a.Version - b.Version })
		for i, r := range rs {
			switch {
			case r.Version != i+1:
				return fmt.Errorf("heldout: %s's label of %s has versions that are not 1, 2, 3...", k[0], k[1])
			case i == 0 && (r.Supersedes != 0 || r.Reason != ""):
				return fmt.Errorf("heldout: the first version of %s's label of %s supersedes nothing", k[0], k[1])
			case i > 0 && (r.Supersedes != rs[i-1].Version || strings.TrimSpace(r.Reason) == ""):
				return fmt.Errorf("heldout: version %d of %s's label of %s must name the version it supersedes (%d) and say why", r.Version, k[0], k[1], rs[i-1].Version)
			}
		}
	}
	return nil
}

// Settle derives the gold from independent labels and adjudications.
func Settle(c Corpus, recs []Record, adjs []Adjudication, t Thresholds) Gold {
	g := Gold{Tokens: map[string]map[string][]string{}, Labelled: map[string]bool{}, Slices: map[string][]string{}, Kinds: map[string][]string{}, Agreed: map[string]int{}, Compared: map[string]int{}, Raw: map[string]map[string]bool{}}
	indep := map[string]bool{}
	for _, l := range c.Labellers {
		indep[l.ID] = l.Independent
	}
	latest, sup := Latest(recs)
	g.Superseded = sup
	byItem := map[string][]Record{}
	for k, r := range latest {
		if !indep[k[0]] {
			continue // a label by someone not attested independent is not evidence
		}
		byItem[k[1]] = append(byItem[k[1]], r)
	}
	adj := map[[2]string]Adjudication{}
	for _, a := range adjs {
		adj[[2]string{a.Item, a.Stage}] = a
	}
	for _, it := range c.Items {
		rs := byItem[it.ID]
		slices.SortFunc(rs, func(a, b Record) int { return strings.Compare(a.Labeller, b.Labeller) })
		// two independent readings are the least that can show a disagreement
		if len(rs) < max(t.MinAnnotators, 2) {
			continue
		}
		g.Labelled[it.ID] = true
		g.Tokens[it.ID] = map[string][]string{}
		g.Raw[it.ID] = map[string]bool{}
		sl := map[string]bool{}
		kinds := map[string]bool{}
		for _, r := range rs {
			for _, s := range r.Label.Slices {
				sl[s] = true
			}
			for _, cl := range r.Label.Claims {
				kinds[cl.Kind] = true
			}
		}
		g.Slices[it.ID] = keys(sl)
		g.Kinds[it.ID] = keys(kinds)
		for _, st := range Stages {
			vals := map[string][]string{}
			agree := true
			for _, r := range rs {
				vals[r.Labeller] = Tokens(r.Label, st)
				if !slices.Equal(vals[r.Labeller], vals[rs[0].Labeller]) {
					agree = false
				}
			}
			g.Compared[st]++
			g.Raw[it.ID][st] = agree
			if agree {
				g.Agreed[st]++
				g.Tokens[it.ID][st] = vals[rs[0].Labeller]
				continue
			}
			d := Disagreement{Item: it.ID, Stage: st, Values: vals}
			if a, ok := adj[[2]string{it.ID, st}]; ok {
				switch {
				case !indep[a.By]:
					d.Problem = fmt.Sprintf("%s is not attested independent", a.By)
				case slices.ContainsFunc(rs, func(r Record) bool { return r.Labeller == a.By }):
					d.Problem = fmt.Sprintf("%s labelled this item and cannot settle their own disagreement", a.By)
				case strings.TrimSpace(a.Rationale) == "":
					d.Problem = "an adjudication needs a rationale"
				default:
					d.Resolved, d.By, d.Why = true, a.By, a.Rationale
					d.Tokens = slices.Compact(slices.Sorted(slices.Values(a.Tokens)))
					g.Tokens[it.ID][st] = d.Tokens
				}
			}
			g.Disagreements = append(g.Disagreements, d)
		}
	}
	sort.Slice(g.Disagreements, func(i, j int) bool {
		a, b := g.Disagreements[i], g.Disagreements[j]
		if a.Item != b.Item {
			return a.Item < b.Item
		}
		return a.Stage < b.Stage
	})
	return g
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ---- predictions ----

// Prediction is what the program reads of an item.
type Prediction struct {
	Item    string
	Label   ItemLabel
	Cluster string
}

// Texts resolves an item's text: a corpus item keeps its own when the licence
// lets it, and otherwise the local store is asked by article ID.
type Texts func(Item) (title, summary string, err error)

// Predict reads every item with the program's own extractor, and groups the
// articles into events with its own clustering.
func Predict(c Corpus, texts Texts) (map[string]Prediction, map[string]string) {
	items := c.Items
	out := map[string]Prediction{}
	skipped := map[string]string{}
	var arts []model.Article
	ids := map[string]string{}
	for _, it := range items {
		title, summary := it.Title, it.Summary
		if it.Licence != Redistributable || (title == "" && summary == "") {
			var err error
			if texts == nil {
				skipped[it.ID] = "the text is not kept in the corpus and no article store was given"
				continue
			}
			if title, summary, err = texts(it); err != nil {
				skipped[it.ID] = err.Error()
				continue
			}
		}
		if ContentHash(title, summary) != it.Hash {
			skipped[it.ID] = "the text no longer matches the content hash it was labelled with"
			continue
		}
		pub := parseTime(it.Published)
		res := assertion.Extract(assertion.Input{ArticleID: it.ID, Title: title, Summary: summary, Form: it.Form, Items: it.Filing, Tickers: it.Tickers, Published: pub, Names: func(t string) []string { return c.Names[t] }})
		out[it.ID] = Prediction{Item: it.ID, Label: fromAssertions(res)}
		ids[it.ID] = it.ID
		arts = append(arts, model.Article{ID: it.ID, SourceID: it.SourceID, URL: it.URL, Title: title, Summary: summary, Published: pub, Tickers: it.Tickers})
	}
	for _, a := range cluster.Assign(arts) {
		p := out[a.ID]
		p.Cluster = a.ClusterID
		out[a.ID] = p
	}
	return out, skipped
}

func parseTime(s string) time.Time {
	for _, f := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func fromAssertions(r assertion.Result) ItemLabel {
	var l ItemLabel
	for _, a := range r.Assertions {
		c := ClaimLabel{Kind: string(a.Kind), Status: string(a.Status), Negated: a.Negated, Denied: a.Denied, Historical: a.Historical, Attribution: string(a.Attribution), Action: a.Action}
		for _, p := range a.Parties {
			if p.Role == assertion.Subject || p.Role == assertion.Counterparty {
				c.Parties = append(c.Parties, PartyLabel{Ticker: p.Ticker, Role: string(p.Role)})
			}
		}
		l.Claims = append(l.Claims, c)
	}
	l.NoClaims = len(l.Claims) == 0
	return l
}

// ---- metrics ----

// Interval is a 95% Wilson score interval.
type Interval struct{ Value, Low, High float64 }

// Wilson is the interval for k successes in n trials; with no trials it is
// 0 to 1, which is to say nothing is known.
func Wilson(k, n int) Interval {
	if n == 0 {
		return Interval{Low: 0, High: 1}
	}
	const z = 1.959963984540054
	p := float64(k) / float64(n)
	d := 1 + z*z/float64(n)
	c := p + z*z/(2*float64(n))
	h := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n)))
	return Interval{Value: p, Low: math.Max(0, (c-h)/d), High: math.Min(1, (c+h)/d)}
}

// Counts of one stage in one group.
type Counts struct {
	Items     int      `json:"items"`
	Exact     int      `json:"exact"`
	TP        int      `json:"tp"`
	FP        int      `json:"fp"`
	FN        int      `json:"fn"`
	Contested int      `json:"contested"`
	Precision Interval `json:"precision"`
	Recall    Interval `json:"recall"`
	ExactRate Interval `json:"exact_rate"`
}

func (c *Counts) finish() {
	c.Precision = Wilson(c.TP, c.TP+c.FP)
	c.Recall = Wilson(c.TP, c.TP+c.FN)
	c.ExactRate = Wilson(c.Exact, c.Items)
}

func (c *Counts) add(gold, pred []string) {
	c.Items++
	if slices.Equal(gold, pred) {
		c.Exact++
	}
	for _, t := range pred {
		if slices.Contains(gold, t) {
			c.TP++
		} else {
			c.FP++
		}
	}
	for _, t := range gold {
		if !slices.Contains(pred, t) {
			c.FN++
		}
	}
}

// GateStatus of a stage or of the whole.
type GateStatus string

const (
	Pass         GateStatus = "pass"
	Fail         GateStatus = "fail"
	Insufficient GateStatus = "insufficient"
	NoLabels     GateStatus = "no_external_labels"
	NotFrozen    GateStatus = "not_frozen"
	Changed      GateStatus = "changed_after_freeze"
	NoThresholds GateStatus = "thresholds_not_stated"
)

// StageResult is one stage over the independent items.
type StageResult struct {
	Stage  string            `json:"stage"`
	All    Counts            `json:"all"`
	Groups map[string]Counts `json:"groups,omitempty"`
	// Agreement is how often the independent annotators agreed before any adjudication.
	Agreement Interval   `json:"annotator_agreement"`
	Gate      GateStatus `json:"gate"`
	Why       string     `json:"why,omitempty"`
}

// Report is the result, with independent evidence and synthetic regressions apart.
type Report struct {
	Version     string        `json:"version"`
	Corpus      string        `json:"corpus"`
	Independent []StageResult `json:"independent"`
	Synthetic   []StageResult `json:"synthetic_regressions"`
	// SyntheticCounts says in words that the synthetic section is not evidence.
	SyntheticNote string            `json:"synthetic_note"`
	Gate          GateStatus        `json:"gate"`
	GateWhy       []string          `json:"gate_why"`
	Items         int               `json:"items"`
	Labelled      int               `json:"independently_labelled_items"`
	Open          int               `json:"open_disagreements"`
	Resolved      int               `json:"resolved_disagreements"`
	Superseded    int               `json:"superseded_label_versions"`
	Skipped       map[string]string `json:"skipped_items,omitempty"`
	Disagreements []Disagreement    `json:"disagreements,omitempty"`
}

// FreezeHash binds the corpus, every label version, every adjudication and the
// thresholds, so that none can change after the figures are seen.
func FreezeHash(c Corpus, recs []Record, adjs []Adjudication, t Thresholds) string {
	slices.SortFunc(recs, func(a, b Record) int {
		return strings.Compare(fmt.Sprintf("%s|%s|%06d", a.Labeller, a.Item, a.Version), fmt.Sprintf("%s|%s|%06d", b.Labeller, b.Item, b.Version))
	})
	slices.SortFunc(adjs, func(a, b Adjudication) int { return strings.Compare(a.Item+"|"+a.Stage, b.Item+"|"+b.Stage) })
	b, _ := json.Marshal(struct {
		C Corpus
		R []Record
		A []Adjudication
		T Thresholds
	}{c, recs, adjs, t})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Run is everything an evaluation reads.
type Run struct {
	Corpus      Corpus
	Records     []Record
	Adjudicated []Adjudication
	Thresholds  Thresholds
	// Frozen is the FreezeHash recorded when the labels and thresholds were frozen;
	// empty when they were not.
	Frozen      string
	Predictions map[string]Prediction
	Skipped     map[string]string
}

// Evaluate produces the report.
func Evaluate(r Run) Report {
	rep := Report{Version: Version, Corpus: r.Corpus.Name, Items: len(r.Corpus.Items), Skipped: r.Skipped,
		SyntheticNote: "synthetic regressions show that known cases still read as before; they are not evidence of accuracy on real news and never count toward the gate"}
	t := r.Thresholds
	tErr := t.Validate()
	g := Settle(r.Corpus, r.Records, r.Adjudicated, t)
	rep.Superseded = g.Superseded
	for _, d := range g.Disagreements {
		if d.Resolved {
			rep.Resolved++
		} else {
			rep.Open++
		}
	}
	rep.Disagreements = g.Disagreements

	syn := map[string]bool{}
	for _, it := range r.Corpus.Items {
		syn[it.ID] = it.Synthetic
	}
	for _, real := range []bool{true, false} {
		var out []StageResult
		for _, st := range Stages {
			sr := StageResult{Stage: st, Groups: map[string]Counts{}}
			all := Counts{}
			groups := map[string]*Counts{}
			agreed, compared := 0, 0
			if st == Identity {
				identity(&sr, r, g, syn, real)
				out = append(out, sr)
				continue
			}
			for _, it := range r.Corpus.Items {
				if syn[it.ID] == real || !g.Labelled[it.ID] {
					continue
				}
				p, ok := r.Predictions[it.ID]
				if !ok {
					continue
				}
				compared++
				if g.Raw[it.ID][st] {
					agreed++
				}
				gold, settled := g.Tokens[it.ID][st]
				if !settled {
					all.Contested++
					continue
				}
				pred := Tokens(p.Label, st)
				all.add(gold, pred)
				for _, grp := range itemGroups(it, g) {
					c := groups[grp]
					if c == nil {
						c = &Counts{}
						groups[grp] = c
					}
					c.add(gold, pred)
				}
			}
			all.finish()
			sr.All = all
			for k, c := range groups {
				c.finish()
				sr.Groups[k] = *c
			}
			sr.Agreement = Wilson(agreed, compared)
			out = append(out, sr)
		}
		if real {
			rep.Independent = out
		} else {
			rep.Synthetic = out
		}
	}
	for _, it := range r.Corpus.Items {
		if g.Labelled[it.ID] && !it.Synthetic {
			rep.Labelled++
		}
	}
	gate(&rep, r, t, tErr)
	return rep
}

func itemGroups(it Item, g Gold) []string {
	var out []string
	for _, k := range g.Kinds[it.ID] {
		out = append(out, "kind:"+k)
	}
	for _, s := range g.Slices[it.ID] {
		out = append(out, "slice:"+s)
	}
	if it.Language != "" {
		out = append(out, "language:"+it.Language)
	}
	if it.SourceID != "" {
		out = append(out, "source:"+it.SourceID)
	}
	return out
}

// identity scores pairs of items: the same event or not.
func identity(sr *StageResult, r Run, g Gold, syn map[string]bool, real bool) {
	var ids []string
	for _, it := range r.Corpus.Items {
		if syn[it.ID] != real && g.Labelled[it.ID] {
			if _, ok := r.Predictions[it.ID]; ok {
				if _, settled := g.Tokens[it.ID][Identity]; settled {
					ids = append(ids, it.ID)
				} else {
					sr.All.Contested++
				}
			}
		}
	}
	c := Counts{}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := g.Tokens[ids[i]][Identity], g.Tokens[ids[j]][Identity]
			if len(a) == 0 || len(b) == 0 {
				continue // an item the labellers placed in no event has no pair relation
			}
			goldSame := a[0] == b[0]
			predSame := r.Predictions[ids[i]].Cluster == r.Predictions[ids[j]].Cluster
			c.Items++
			switch {
			case goldSame && predSame:
				c.TP++
				c.Exact++
			case predSame:
				c.FP++
			case goldSame:
				c.FN++
			default:
				c.Exact++
			}
		}
	}
	c.Contested = sr.All.Contested
	c.finish()
	sr.All = c
	ag, cmp := 0, 0
	for _, it := range r.Corpus.Items {
		if raw, ok := g.Raw[it.ID]; ok && syn[it.ID] != real {
			cmp++
			if raw[Identity] {
				ag++
			}
		}
	}
	sr.Agreement = Wilson(ag, cmp)
}

func gate(rep *Report, r Run, t Thresholds, tErr error) {
	why := func(f string, a ...any) { rep.GateWhy = append(rep.GateWhy, fmt.Sprintf(f, a...)) }
	for i := range rep.Independent {
		s := &rep.Independent[i]
		s.Gate = Insufficient
		switch {
		case rep.Labelled == 0:
			s.Gate, s.Why = NoLabels, "no item has independent labels from enough annotators"
		case tErr != nil:
			s.Gate, s.Why = NoThresholds, tErr.Error()
		case s.All.Items < t.MinItems:
			s.Why = fmt.Sprintf("%d settled items, %d needed", s.All.Items, t.MinItems)
		default:
			// A stage with nothing to find or to call cannot be judged on that side.
			switch {
			case s.All.TP+s.All.FP == 0 || s.All.TP+s.All.FN == 0:
				s.Why = "no claims to score on one side: precision or recall is undefined"
			case s.All.Precision.Low < t.MinPrecision:
				s.Gate, s.Why = Fail, fmt.Sprintf("precision lower bound %.3f is below %.3f", s.All.Precision.Low, t.MinPrecision)
			case s.All.Recall.Low < t.MinRecall:
				s.Gate, s.Why = Fail, fmt.Sprintf("recall lower bound %.3f is below %.3f", s.All.Recall.Low, t.MinRecall)
			default:
				s.Gate = Pass
			}
		}
	}
	rep.Gate = Pass
	switch {
	case rep.Labelled == 0:
		rep.Gate = NoLabels
		why("no independently labelled item: a missing external label is not a pass")
	case tErr != nil:
		rep.Gate = NoThresholds
		why("%v", tErr)
	case r.Frozen == "":
		rep.Gate = NotFrozen
		why("the labels and thresholds were not frozen before this evaluation")
	case r.Frozen != FreezeHash(r.Corpus, slices.Clone(r.Records), slices.Clone(r.Adjudicated), t):
		rep.Gate = Changed
		why("the corpus, labels, adjudications or thresholds differ from what was frozen")
	}
	if rep.Gate == Pass {
		failed, short := false, false
		for _, s := range rep.Independent {
			switch s.Gate {
			case Fail:
				failed = true
				why("%s fails: %s", s.Stage, s.Why)
			case Pass:
			default:
				short = true
				why("%s is %s: %s", s.Stage, s.Gate, s.Why)
			}
		}
		switch {
		case failed:
			rep.Gate = Fail
		case short:
			rep.Gate = Insufficient
		}
	}
	if rep.Open > 0 && rep.Gate == Pass {
		why("%d disagreement(s) are still open and their items are left out of the figures", rep.Open)
	}
}
