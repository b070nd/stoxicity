package laya

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Question ids, as they appear in a request and in the answers.
const (
	qRelevant    = "relevant"
	qGroup       = "group"
	qKind        = "kind"
	qMateriality = "materiality"
	qTone        = "tone"
	qSector      = "sector"
	qTopic       = "topic"
)

// Answer types, as laya-serve names them.
const (
	typeNoul   = "noul"
	typeChoice = "choice"
	typeScore  = "score"
)

// Keys that are always offered next to the configured sectors and topics.
const (
	keyNone  = "none"
	keyMacro = "macro"
)

// question is one typed question in a request. Criteria is a map of key to
// description for a choice and a list of level descriptions for a score.
type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// groupOrder is the order in which the groups are listed to the model and in
// which the second round asks about them.
var groupOrder = []model.EventGroup{
	model.GroupResults, model.GroupGuidance, model.GroupCapital, model.GroupCorporate,
	model.GroupGovernance, model.GroupIntegrity, model.GroupOwnership, model.GroupBusiness,
	model.GroupMacro, model.GroupNoise,
}

// groupDescriptions says in one line what each event group covers.
var groupDescriptions = map[model.EventGroup]string{
	model.GroupResults:    "Reported financial results: quarterly or annual earnings, revenue, profit, margins, annual and quarterly reports",
	model.GroupGuidance:   "The company's own forecast or outlook for coming periods, raised, cut or confirmed",
	model.GroupCapital:    "How the company spends and raises money: share buybacks, dividends, new shares, borrowing, spin-offs",
	model.GroupCorporate:  "Deals and restructuring: acquisitions, mergers, major contracts, layoffs, plant closures, write-downs",
	model.GroupGovernance: "People and control: executive or director changes, change of control, bylaws, shareholder votes",
	model.GroupIntegrity:  "Reliability of the numbers and the listing: restatements, auditor changes, cyber incidents, delisting, bankruptcy",
	model.GroupOwnership:  "Who is buying and selling: insider trades, large holder stakes, fund holdings",
	model.GroupBusiness:   "The operating business: products, competition, company-specific regulation, lawsuits",
	model.GroupMacro:      "The economy and whole industries: interest rates, inflation, central banks, industry-wide regulation",
	model.GroupNoise:      "Little new information: stock price chatter, analyst price targets and ratings, opinion pieces, anything else",
}

// kindDescriptions says in one line what each event kind is.
var kindDescriptions = map[model.EventKind]string{
	model.EventResults:           "Reported results for a quarter or a year: earnings, revenue, profit, annual or quarterly report",
	model.EventGuidance:          "The company's outlook or forecast for coming periods, raised, cut or confirmed",
	model.EventBuyback:           "A share repurchase program announced, changed or completed",
	model.EventDividend:          "A dividend declared, raised, cut, suspended or paid as a special one",
	model.EventIssuance:          "New shares or convertible securities sold, which dilutes existing owners",
	model.EventDebt:              "Borrowing: bonds, loans, credit facilities, covenants, or a new debt obligation",
	model.EventSpinoff:           "A business unit separated into its own listed company",
	model.EventAcquisition:       "The company buys or merges with another business or a large asset",
	model.EventAgreement:         "A significant contract or partnership entered into or ended",
	model.EventRestructuring:     "Layoffs, plant closures, exit from a business or a cost-cutting program",
	model.EventImpairment:        "A write-down of assets or goodwill",
	model.EventOfficerChange:     "A CEO, CFO, other senior officer or director appointed, resigns or is dismissed",
	model.EventControlChange:     "A person or group gains or loses control of the company",
	model.EventBylaws:            "The company's charter or bylaws amended",
	model.EventVote:              "The result of a shareholder meeting or vote",
	model.EventNonReliance:       "A restatement: earlier financial statements should no longer be relied on",
	model.EventAuditorChange:     "The external auditor changed, or disagreed with the company",
	model.EventCyber:             "A cybersecurity incident, data breach or ransomware attack at the company",
	model.EventDelisting:         "Notice of delisting, or failure to meet a stock exchange listing rule",
	model.EventBankruptcy:        "A bankruptcy, insolvency or creditor protection filing",
	model.EventInsiderBuy:        "An executive or director buys shares of their own company",
	model.EventInsiderSell:       "An executive or director sells shares of their own company",
	model.Event13DG:              "A large holder discloses or changes a stake of five percent or more",
	model.Event13F:               "An institutional investor's quarterly holdings change",
	model.EventProduct:           "Products: launches, recalls, outages, or demand for what the company sells",
	model.EventCompetitive:       "Competitors, market share, price pressure, or customers won or lost",
	model.EventCompanyRegulatory: "A regulator's action, approval, probe or fine aimed at this company",
	model.EventLitigation:        "Lawsuits, settlements and court rulings involving the company",
	model.EventRates:             "Central bank decisions and interest rate moves",
	model.EventInflation:         "Inflation, prices, wages and other economy-wide data",
	model.EventSectorRegulation:  "New laws, tariffs or rules for a whole industry or country",
	model.EventPriceChatter:      "Stock price moves, trading volume, options activity, technical analysis or market commentary",
	model.EventAnalystTgt:        "An analyst raises, cuts or starts a rating or price target",
	model.EventOther:             "None of the other kinds fits",
}

// questionSet is every question a Scorer can ask, built once from its
// configuration.
type questionSet struct {
	// round1 is asked of every article.
	round1 map[string]question
	// kinds holds the second-round question for each group.
	kinds map[model.EventGroup]map[string]question
	// sectors and topics are the cleaned, sorted configured names.
	sectors []string
	topics  []string
}

// buildQuestions writes the questions for the given sector and topic lists.
func buildQuestions(sectors, topics []string) questionSet {
	qs := questionSet{
		round1:  make(map[string]question),
		kinds:   make(map[model.EventGroup]map[string]question),
		sectors: cleanNames(sectors),
		topics:  cleanNames(topics),
	}

	groups := make(map[string]string, len(groupOrder))
	for _, g := range groupOrder {
		groups[string(g)] = groupDescriptions[g]
	}

	qs.round1[qRelevant] = question{
		Type: typeNoul,
		Instructions: "You are reading one news item for an investor who buys businesses to hold for many years. " +
			"Answer true if it bears on the long-term value of a company, an industry or the economy: " +
			"earnings power, competitive position, balance sheet, how management allocates capital, or whether the reported numbers can be trusted. " +
			"Answer false for stock price moves, trading chatter, analyst price targets, advertising, lists of stocks, " +
			"and commentary that tells the owner of a business nothing new.",
	}
	qs.round1[qGroup] = question{
		Type: typeChoice,
		Instructions: "Choose the one family of events that best describes the main fact in this news item. " +
			"Judge by what happened, not by the company's industry or the mood of the text. " +
			"A restatement of earlier results is about the reliability of the numbers, not about results. " +
			"A change in an analyst's rating or price target, or talk about the share price, belongs to noise.",
		Criteria: groups,
	}
	qs.round1[qMateriality] = question{
		Type: typeScore,
		Instructions: "How much should this news change a long-term owner's estimate of what the business is worth? " +
			"Think about the next ten years, not the next day: judge the effect on earnings power, durability, " +
			"per-share value and trust in the numbers. Pick the level that fits best; most news is on the low levels.",
		Criteria: append([]string(nil), model.MaterialityLevels[1:]...),
	}
	qs.round1[qTone] = question{
		Type: typeScore,
		Instructions: "Is this news good or bad for the long-term value of the business it is about? " +
			"Judge the effect on the owners of the business, not the mood of the wording. " +
			"A fact that does not clearly help or hurt the business is neutral. " +
			"An analyst's rating or price target is an opinion, not a fact about the business, so it is neutral.",
		Criteria: []string{
			"very negative for the business",
			"negative for the business",
			"neutral, or good and bad in equal measure",
			"positive for the business",
			"very positive for the business",
		},
	}
	if len(qs.sectors) > 0 {
		criteria := map[string]string{
			keyMacro: "The economy as a whole rather than one sector: interest rates, inflation, currencies, trade",
			keyNone:  "No sector fits, or the item is not about a business or an industry",
		}
		for _, s := range qs.sectors {
			criteria[s] = "Companies and news in the " + s + " sector"
		}
		qs.round1[qSector] = question{
			Type: typeChoice,
			Instructions: "Which sector does the company or industry this news is about belong to? " +
				"Choose macro when the news is about the economy as a whole, and none when no sector fits.",
			Criteria: criteria,
		}
	}
	if len(qs.topics) > 0 {
		criteria := map[string]string{
			keyNone: "None of the themes fits clearly",
		}
		for _, t := range qs.topics {
			criteria[t] = "News about " + t
		}
		qs.round1[qTopic] = question{
			Type: typeChoice,
			Instructions: "Does this news item bear clearly on one of these themes the investor follows? " +
				"Choose the one theme it is mostly about. Choose none unless the connection is clear and direct.",
			Criteria: criteria,
		}
	}

	for _, g := range groupOrder {
		criteria := make(map[string]string)
		for _, k := range model.KindsIn(g) {
			criteria[string(k)] = kindDescriptions[k]
		}
		qs.kinds[g] = map[string]question{
			qKind: {
				Type: typeChoice,
				Instructions: "This news item belongs to this family of events: " + groupDescriptions[g] + ". " +
					"Choose the one event within it that best describes the main fact. " +
					"Judge by what happened, not by the mood of the text.",
				Criteria: criteria,
			},
		}
	}
	return qs
}

// cleanNames trims the names, drops empty ones, the reserved keys and
// duplicates, and sorts what is left.
func cleanNames(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" || strings.EqualFold(n, keyNone) || strings.EqualFold(n, keyMacro) || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// fingerprint is the first 8 hex characters of a hash of the whole question
// set: the instructions, the criteria and the sorted sector and topic lists.
func (qs questionSet) fingerprint() string {
	canon := struct {
		Round1  map[string]question
		Kinds   map[model.EventGroup]map[string]question
		Sectors []string
		Topics  []string
	}{qs.round1, qs.kinds, qs.sectors, qs.topics}
	// Only strings, maps of strings and slices of strings are marshalled, so
	// this cannot fail; map keys come out sorted, which makes the bytes stable.
	b, _ := json.Marshal(canon)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}
