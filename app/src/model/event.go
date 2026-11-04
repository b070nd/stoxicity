package model

import "sort"

// EventGroup is a coarse family of events. A classifier with a small option
// budget (laya handles about 20 options per question) asks for the group
// first and the kind within it second.
type EventGroup string

const (
	GroupResults    EventGroup = "results"
	GroupGuidance   EventGroup = "guidance"
	GroupCapital    EventGroup = "capital"    // how the company spends and raises money
	GroupCorporate  EventGroup = "corporate"  // deals and restructuring
	GroupGovernance EventGroup = "governance" // people and control
	GroupIntegrity  EventGroup = "integrity"  // the reliability of the numbers and the listing
	GroupOwnership  EventGroup = "ownership"  // who is buying and selling
	GroupBusiness   EventGroup = "business"
	GroupMacro      EventGroup = "macro"
	GroupNoise      EventGroup = "noise"
)

// EventKind is what an article is about, from a fixed list so it can be
// filtered and drawn on a timeline. Where a kind maps to an SEC Form 8-K item,
// the item is noted; the item number is the strongest signal available.
type EventKind string

const (
	EventResults EventKind = "results" // 8-K 2.02, 10-K, 10-Q

	EventGuidance EventKind = "guidance" // 8-K 7.01 or 2.02 text

	EventBuyback  EventKind = "buyback"
	EventDividend EventKind = "dividend"
	EventIssuance EventKind = "issuance" // 8-K 3.02 unregistered sales of equity
	EventDebt     EventKind = "debt"     // 8-K 2.03, 2.04
	EventSpinoff  EventKind = "spinoff"

	EventAcquisition   EventKind = "acquisition"   // 8-K 2.01
	EventAgreement     EventKind = "agreement"     // 8-K 1.01, 1.02
	EventRestructuring EventKind = "restructuring" // 8-K 2.05
	EventImpairment    EventKind = "impairment"    // 8-K 2.06

	EventOfficerChange EventKind = "officer_change" // 8-K 5.02
	EventControlChange EventKind = "control_change" // 8-K 5.01
	EventBylaws        EventKind = "bylaws"         // 8-K 5.03
	EventVote          EventKind = "vote"           // 8-K 5.07

	EventNonReliance   EventKind = "non_reliance"   // 8-K 4.02, a restatement
	EventAuditorChange EventKind = "auditor_change" // 8-K 4.01
	EventCyber         EventKind = "cyber"          // 8-K 1.05
	EventDelisting     EventKind = "delisting"      // 8-K 3.01
	EventBankruptcy    EventKind = "bankruptcy"     // 8-K 1.03

	EventInsiderBuy  EventKind = "insider_buy"  // Form 4
	EventInsiderSell EventKind = "insider_sell" // Form 4
	Event13DG        EventKind = "13d_13g"      // large holder disclosures
	Event13F         EventKind = "13f_change"   // quarterly institutional holdings

	EventProduct           EventKind = "product"
	EventCompetitive       EventKind = "competitive"
	EventCompanyRegulatory EventKind = "regulatory_company"
	EventLitigation        EventKind = "litigation"

	EventRates            EventKind = "rates"
	EventInflation        EventKind = "inflation"
	EventSectorRegulation EventKind = "regulation_sector"

	EventPriceChatter EventKind = "price_chatter"
	EventAnalystTgt   EventKind = "analyst_target"
	EventOther        EventKind = "other"
)

type eventInfo struct {
	group EventGroup
	base  int
}

// events is the whole taxonomy. base is where the lens starts when it rates
// materiality, the lower end of the range proposed in docs/research; the
// lens moves it up or down from the text.
var events = map[EventKind]eventInfo{
	EventResults:  {GroupResults, 3},
	EventGuidance: {GroupGuidance, 3},

	EventBuyback: {GroupCapital, 3}, EventDividend: {GroupCapital, 3}, EventIssuance: {GroupCapital, 3},
	EventDebt: {GroupCapital, 3}, EventSpinoff: {GroupCapital, 3},

	EventAcquisition: {GroupCorporate, 3}, EventAgreement: {GroupCorporate, 3},
	EventRestructuring: {GroupCorporate, 3}, EventImpairment: {GroupCorporate, 3},

	EventOfficerChange: {GroupGovernance, 2}, EventControlChange: {GroupGovernance, 2},
	EventBylaws: {GroupGovernance, 2}, EventVote: {GroupGovernance, 2},

	EventNonReliance: {GroupIntegrity, 4}, EventAuditorChange: {GroupIntegrity, 4},
	EventCyber: {GroupIntegrity, 4}, EventDelisting: {GroupIntegrity, 4}, EventBankruptcy: {GroupIntegrity, 4},

	EventInsiderBuy: {GroupOwnership, 2}, EventInsiderSell: {GroupOwnership, 2},
	Event13DG: {GroupOwnership, 2}, Event13F: {GroupOwnership, 2},

	EventProduct: {GroupBusiness, 2}, EventCompetitive: {GroupBusiness, 2},
	EventCompanyRegulatory: {GroupBusiness, 2}, EventLitigation: {GroupBusiness, 2},

	EventRates: {GroupMacro, 2}, EventInflation: {GroupMacro, 2}, EventSectorRegulation: {GroupMacro, 2},

	EventPriceChatter: {GroupNoise, 1}, EventAnalystTgt: {GroupNoise, 1}, EventOther: {GroupNoise, 1},
}

// Valid reports whether k is in the taxonomy.
func (k EventKind) Valid() bool { _, ok := events[k]; return ok }

// Group is the family k belongs to; empty if k is not valid.
func (k EventKind) Group() EventGroup { return events[k].group }

// BaseMateriality is the starting materiality for k; 0 if k is not valid.
func (k EventKind) BaseMateriality() int { return events[k].base }

// Valid reports whether g is a group of the taxonomy.
func (g EventGroup) Valid() bool {
	for _, info := range events {
		if info.group == g {
			return true
		}
	}
	return false
}

// EventKinds returns every kind, sorted by name.
func EventKinds() []EventKind {
	out := make([]EventKind, 0, len(events))
	for k := range events {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// KindsIn returns the kinds of one group, sorted by name.
func KindsIn(g EventGroup) []EventKind {
	var out []EventKind
	for _, k := range EventKinds() {
		if k.Group() == g {
			out = append(out, k)
		}
	}
	return out
}

// MaterialityLevels says what each Score.Materiality value means, indexed 1 to 5
// (index 0 is unused). It is a rubric for the lens and for people labelling
// articles: two people applying it should mostly agree, which is what
// evaluation measures. The "about 10% of market cap" line is a proposal to
// calibrate against labels, not an established threshold.
var MaterialityLevels = [6]string{
	1: "noise: price targets, intraday moves, options flow, commentary with no new fact",
	2: "context: background that does not change an estimate of the business",
	3: "new information on an expected path: results in line, routine acquisition, regular dividend",
	4: "changes durability or capital allocation: buyback or dividend policy, CEO or CFO change, guidance outside its range, large debt raise",
	5: "changes owner earnings, per-share value or trust in the numbers: restatement, bankruptcy, material cyber incident, large dilutive issuance, change of control, acquisition above about 10% of market cap",
}
