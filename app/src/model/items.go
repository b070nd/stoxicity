package model

import "strings"

// ItemRating is what the items of one Form 8-K say about the filing: the kind of
// event, how material it is, and how much the other items add. It is made from
// the item numbers alone (package edgar), because the filing's text is not read.
type ItemRating struct {
	Kind        EventKind
	Materiality int // 1 to 5, that of the most material item
	// Item is the item that decided Kind and Materiality.
	Item string
	// Also are the other items that carry information, in the order they came:
	// an earnings release filed together with a presentation or an officer change.
	Also []string
	// Bonus is the relevance the other items add (see edgar.Rate).
	Bonus float64
}

// itemTitles are the items of Form 8-K as the form words them, and a short
// label for each, for a list line.
var itemTitles = map[string][2]string{
	"1.01": {"Entry into a Material Definitive Agreement", "agreement"},
	"1.02": {"Termination of a Material Definitive Agreement", "agreement ended"},
	"1.03": {"Bankruptcy or Receivership", "bankruptcy"},
	"1.04": {"Mine Safety: Reporting of Shutdowns and Patterns of Violations", "mine safety"},
	"1.05": {"Material Cybersecurity Incidents", "cyber incident"},
	"2.01": {"Completion of Acquisition or Disposition of Assets", "acquisition"},
	"2.02": {"Results of Operations and Financial Condition", "results"},
	"2.03": {"Creation of a Direct Financial Obligation or an Obligation under an Off-Balance Sheet Arrangement", "new debt"},
	"2.04": {"Triggering Events That Accelerate or Increase a Direct Financial Obligation", "debt accelerated"},
	"2.05": {"Costs Associated with Exit or Disposal Activities", "restructuring"},
	"2.06": {"Material Impairments", "impairment"},
	"3.01": {"Notice of Delisting or Failure to Satisfy a Continued Listing Rule or Standard; Transfer of Listing", "delisting notice"},
	"3.02": {"Unregistered Sales of Equity Securities", "share issue"},
	"3.03": {"Material Modification to Rights of Security Holders", "rights changed"},
	"4.01": {"Changes in Registrant's Certifying Accountant", "auditor change"},
	"4.02": {"Non-Reliance on Previously Issued Financial Statements or a Related Audit Report or Completed Interim Review", "non-reliance"},
	"5.01": {"Changes in Control of Registrant", "control change"},
	"5.02": {"Departure of Directors or Certain Officers; Election of Directors; Appointment of Certain Officers; Compensatory Arrangements", "officers and directors"},
	"5.03": {"Amendments to Articles of Incorporation or Bylaws; Change in Fiscal Year", "bylaws"},
	"5.04": {"Temporary Suspension of Trading Under Registrant's Employee Benefit Plans", "plan trading halt"},
	"5.05": {"Amendments to the Registrant's Code of Ethics, or Waiver of a Provision of the Code of Ethics", "ethics code"},
	"5.06": {"Change in Shell Company Status", "shell status"},
	"5.07": {"Submission of Matters to a Vote of Security Holders", "shareholder vote"},
	"5.08": {"Shareholder Director Nominations", "director nominations"},
	"6.01": {"ABS Informational and Computational Material", "asset-backed"},
	"6.02": {"Change of Servicer or Trustee", "asset-backed"},
	"6.03": {"Change in Credit Enhancement or Other External Support", "asset-backed"},
	"6.04": {"Failure to Make a Required Distribution", "asset-backed"},
	"6.05": {"Securities Act Updating Disclosure", "asset-backed"},
	"7.01": {"Regulation FD Disclosure", "Reg FD"},
	"8.01": {"Other Events", "other events"},
	"9.01": {"Financial Statements and Exhibits", "exhibits"},
}

// ItemTitle is the title Form 8-K gives an item and a short label for it; false
// for a number that is no item of the form.
func ItemTitle(item string) (title, short string, ok bool) {
	t, ok := itemTitles[item]
	return t[0], t[1], ok
}

// DescribeItems writes each item with its title: "2.02 Results of Operations
// and Financial Condition". An unknown number is written as it is.
func DescribeItems(items []string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		if title, _, ok := ItemTitle(it); ok {
			out[i] = it + " " + title
		} else {
			out[i] = it
		}
	}
	return out
}

// ItemLabel is the items in a few words for a list line: the short labels of
// the items that say something, at most n of them ("results, Reg FD"). The
// exhibits item, which only attaches documents, is left out.
func ItemLabel(items []string, n int) string {
	var parts []string
	seen := map[string]bool{}
	for _, it := range items {
		if it == "9.01" {
			continue
		}
		_, short, ok := ItemTitle(it)
		if !ok || seen[short] {
			continue
		}
		seen[short] = true
		parts = append(parts, short)
		if len(parts) == n {
			break
		}
	}
	return strings.Join(parts, ", ")
}
