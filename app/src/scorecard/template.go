package scorecard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// The ratios a scorecard can show.
const (
	NetMargin       = "net_margin"
	OperatingMargin = "operating_margin"
	ROE             = "roe"
	DebtToEquity    = "debt_to_equity"
	InterestCover   = "interest_cover"
	PayoutRatio     = "payout_ratio"
	EquityToAssets  = "equity_to_assets"
	FCFMargin       = "fcf_margin"
	FCFConversion   = "fcf_conversion"
	RevenueGrowth   = "revenue_growth"
)

// Ratios lists the ratios in display order.
var Ratios = []string{RevenueGrowth, OperatingMargin, NetMargin, ROE, DebtToEquity, InterestCover, FCFMargin, FCFConversion, PayoutRatio, EquityToAssets}

type ratioDef struct {
	Name     string
	Num, Den string // metrics; "fcf" stands for operating cash flow less capex
	Growth   bool
	Label    string
}

var ratioDefs = map[string]ratioDef{
	NetMargin:       {Name: NetMargin, Num: NetIncome, Den: Revenue, Label: "net income / revenue"},
	OperatingMargin: {Name: OperatingMargin, Num: OperatingIncome, Den: Revenue, Label: "operating income / revenue"},
	ROE:             {Name: ROE, Num: NetIncome, Den: Equity, Label: "net income / equity at year end"},
	DebtToEquity:    {Name: DebtToEquity, Num: TotalDebt, Den: Equity, Label: "total debt / equity"},
	InterestCover:   {Name: InterestCover, Num: OperatingIncome, Den: InterestExpense, Label: "operating income / interest expense"},
	PayoutRatio:     {Name: PayoutRatio, Num: DividendsPaid, Den: NetIncome, Label: "dividends paid / net income"},
	EquityToAssets:  {Name: EquityToAssets, Num: Equity, Den: TotalAssets, Label: "equity / total assets"},
	FCFMargin:       {Name: FCFMargin, Num: "fcf", Den: Revenue, Label: "free cash flow / revenue"},
	FCFConversion:   {Name: FCFConversion, Num: "fcf", Den: NetIncome, Label: "free cash flow / net income"},
	RevenueGrowth:   {Name: RevenueGrowth, Num: Revenue, Den: Revenue, Growth: true, Label: "revenue / prior year revenue - 1"},
}

// Template says which ratios mean something for a kind of business and why the
// others are refused.
type Template struct {
	Name    string
	Summary string
	Ratios  []string
	// Why says, for the ratios left out, why an ordinary-company ratio would mislead.
	Why string
}

var templates = []Template{
	{Name: "ordinary", Summary: "an industrial or commercial company", Ratios: Ratios},
	{Name: "utility", Summary: "a regulated utility: ordinary ratios, read against the sector's high leverage", Ratios: Ratios},
	{Name: "bank", Summary: "a bank", Ratios: []string{ROE, EquityToAssets, PayoutRatio},
		Why: "a bank's revenue, debt and cash flow mean something else (deposits are funding, not debt; interest is cost of goods); regulatory capital and credit quality are not in this data model"},
	{Name: "insurer", Summary: "an insurer", Ratios: []string{ROE, EquityToAssets, PayoutRatio},
		Why: "an insurer's premiums, reserves and investment float are not ordinary revenue and debt; solvency measures are not in this data model"},
	{Name: "reit", Summary: "a real-estate investment trust", Ratios: []string{RevenueGrowth, InterestCover, EquityToAssets},
		Why: "depreciation distorts net income and free cash flow for a REIT; funds from operations (FFO/AFFO), the measure its dividends are judged by, is not in this data model"},
	{Name: "fund", Summary: "a fund or ETF", Ratios: nil,
		Why: "a fund has holdings, not a business: look-through to its constituents, costs and tracking are needed, and none are in this data model"},
	{Name: "unknown", Summary: "the kind of business is not chosen", Ratios: nil,
		Why: "no template has been chosen for this security, so no ratio is computed: choose one with scorecard template"},
}

// Templates lists the sector templates.
func Templates() []Template { return append([]Template(nil), templates...) }

// TemplateByName finds a template.
func TemplateByName(name string) (Template, bool) {
	for _, t := range templates {
		if t.Name == name {
			return t, true
		}
	}
	return Template{}, false
}

func (t Template) supports(ratio string) bool {
	for _, r := range t.Ratios {
		if r == ratio {
			return true
		}
	}
	return false
}

// rulesDigest identifies the ratio definitions and the templates: a change to
// either changes every scorecard's hash.
func rulesDigest() string {
	var b strings.Builder
	names := make([]string, 0, len(ratioDefs))
	for n := range ratioDefs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := ratioDefs[n]
		fmt.Fprintf(&b, "ratio %s %s %s %v\n", n, d.Num, d.Den, d.Growth)
	}
	for _, t := range templates {
		fmt.Fprintf(&b, "template %s %s\n", t.Name, strings.Join(t.Ratios, ","))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}
