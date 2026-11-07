package edgar

import (
	"regexp"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// itemKinds maps the Form 8-K item numbers that say something about a company
// to the event kind they become. Item numbers are the strongest signal of what
// happened. The table is checked against the full form by a test, which also
// checks that each item is either here or in ignoredItems.
var itemKinds = map[string]model.EventKind{
	"1.01": model.EventAgreement,
	"1.02": model.EventAgreement,
	"1.03": model.EventBankruptcy,
	"1.05": model.EventCyber,

	"2.01": model.EventAcquisition,
	"2.02": model.EventResults,
	"2.03": model.EventDebt,
	"2.04": model.EventDebt,
	"2.05": model.EventRestructuring,
	"2.06": model.EventImpairment,

	"3.01": model.EventDelisting,
	"3.02": model.EventIssuance,

	"4.01": model.EventAuditorChange,
	"4.02": model.EventNonReliance,

	"5.01": model.EventControlChange,
	"5.02": model.EventOfficerChange,
	"5.03": model.EventBylaws,
	"5.07": model.EventVote,

	"7.01": model.EventOther,
	"8.01": model.EventOther,
}

// itemMateriality is how much each mapped item matters on the scale of
// model.MaterialityLevels, judged from the item alone: a filing under it may be
// worth more or less, but the number says what the form requires its filer to
// report. 1.05 and 1.03 and 4.02 and 5.01 are reported only when they are
// serious, so they start high; 5.02 covers a CEO leaving as well as a director
// being elected, and 7.01 and 8.01 are what a company files when no other item
// fits, so they start low.
var itemMateriality = map[string]int{
	"1.01": 3, "1.02": 3, "1.03": 5, "1.05": 5,
	"2.01": 3, "2.02": 3, "2.03": 3, "2.04": 4, "2.05": 3, "2.06": 4,
	"3.01": 4, "3.02": 3,
	"4.01": 3, "4.02": 5,
	"5.01": 5, "5.02": 2, "5.03": 1, "5.07": 1,
	"7.01": 1, "8.01": 1,
}

// extraBonus is the relevance each further item of another kind adds, up to
// three: an earnings release that comes with a presentation (7.01) or an officer
// change (5.02) says more than one that comes alone.
const extraBonus = 0.02

// Rate reads the items of a filing: the most material mapped item decides the
// kind and the materiality (the first of equals, and the more material kind
// first), and every other mapped item of another kind adds a little relevance.
// It returns false if no item maps, and the text is read instead.
func Rate(items []string) (model.ItemRating, bool) {
	var r model.ItemRating
	best := ""
	for _, it := range items {
		m, ok := itemMateriality[it]
		if !ok {
			continue
		}
		if best == "" || m > r.Materiality {
			best, r.Materiality = it, m
		}
	}
	if best == "" {
		return r, false
	}
	r.Item, r.Kind = best, itemKinds[best]
	seen := map[string]bool{best: true}
	kinds := map[model.EventKind]bool{r.Kind: true}
	for _, it := range items {
		k, ok := itemKinds[it]
		if !ok || seen[it] {
			continue
		}
		seen[it] = true
		if !kinds[k] {
			kinds[k] = true
			r.Also = append(r.Also, it)
		}
	}
	r.Bonus = extraBonus * float64(min(len(r.Also), 3))
	return r, true
}

// ignoredItems lists the Form 8-K items that have no useful event kind, with
// the reason for each. They are deliberately not mapped.
var ignoredItems = map[string]string{
	"1.04": "mine safety notices, relevant only to mining companies",
	"3.03": "material change to holders' rights, no kind fits",
	"5.04": "suspension of trading under an employee benefit plan, procedural",
	"5.05": "amendment to the code of ethics, no kind fits",
	"5.06": "change in shell company status, no kind fits",
	"5.08": "date of the annual meeting for director nominations, procedural",
	"6.01": "asset-backed securities report, not about the issuer's own business",
	"6.02": "change of asset-backed securities servicer or trustee, not about the business",
	"6.03": "change in credit enhancement of asset-backed securities, not about the business",
	"6.04": "failure to make a required asset-backed securities distribution, not about the business",
	"6.05": "updated disclosure for asset-backed securities, not about the business",
	"9.01": "financial statements and exhibits, which carry the substance of another item",
}

// itemPattern matches a candidate item number: one digit, a dot, two digits.
var itemPattern = regexp.MustCompile(`\d\.\d\d`)

// ItemKind returns the event kind for one Form 8-K item number such as "2.02".
// It returns false for an item listed as ignored, and for an unknown item.
func ItemKind(item string) (model.EventKind, bool) {
	k, ok := itemKinds[item]
	return k, ok
}

// Kinds returns the event kind of a filing from its item numbers: the kind with
// the highest base materiality among the items that map. Where two items tie,
// the one that comes first in items wins. It returns false if no item maps.
func Kinds(items []string) (model.EventKind, bool) {
	var best model.EventKind
	found := false
	for _, item := range items {
		k, ok := ItemKind(item)
		if !ok {
			continue
		}
		if !found || k.BaseMateriality() > best.BaseMateriality() {
			best = k
			found = true
		}
	}
	return best, found
}

// ParseItems extracts Form 8-K item numbers from text such as "Item 2.02, Item
// 9.01", "2.02,9.01" or "Items 5.02 and 9.01". Each number is returned once, in
// the order it first appears. A number is only taken when it is not part of a
// longer number, a dotted version such as 1.2.02, or a dollar amount.
func ParseItems(s string) []string {
	const digits = "0123456789"
	var out []string
	seen := map[string]bool{}
	for _, loc := range itemPattern.FindAllStringIndex(s, -1) {
		if loc[0] > 0 && strings.ContainsAny(s[loc[0]-1:loc[0]], digits+".$") {
			continue
		}
		if loc[1] < len(s) && strings.ContainsAny(s[loc[1]:loc[1]+1], digits) {
			continue
		}
		item := s[loc[0]:loc[1]]
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}
