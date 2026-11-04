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
