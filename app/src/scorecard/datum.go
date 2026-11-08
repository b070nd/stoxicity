// Package scorecard turns reported business figures into a multi-year, sourced
// assessment a person can read before a buy proposal. Every number carries where
// it came from; a figure that is not there stays visibly missing and a ratio
// whose inputs do not fit (another currency, another period, a zero or negative
// denominator) is reported as such instead of being computed anyway.
//
// Conventions, stated once:
//
//   - Figures are exact decimals in whole currency units, never in thousands.
//   - A ratio is only computed for a business the sector template supports. A
//     bank, insurer, REIT or fund has its own measures (regulatory capital,
//     funds from operations, look-through holdings) that are not in this data
//     model, so ordinary-company ratios are refused for them, not approximated.
//   - A figure counts as known only from the day it was filed. Reading a
//     scorecard "as of" a day uses the newest version filed by then, so a
//     restatement filed later cannot leak into an earlier reading.
//   - Keyword table, extractor and rule versions are part of the content hash
//     of every scorecard, so a change to any of them is visible as a change of
//     hash and is written into plan manifests.
package scorecard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

// The metrics a Datum can be of. Flows cover a period, stocks a date.
const (
	Revenue           = "revenue"
	OperatingIncome   = "operating_income"
	Ebitda            = "ebitda"
	NetIncome         = "net_income"
	OperatingCashFlow = "operating_cash_flow"
	Capex             = "capex"
	InterestExpense   = "interest_expense"
	DividendsPaid     = "dividends_paid"
	TotalAssets       = "total_assets"
	Equity            = "equity"
	TotalDebt         = "total_debt"
	Cash              = "cash"
	Shares            = "shares" // a count, not money
)

type metricInfo struct {
	flow  bool // covers a period; otherwise a value at a date
	count bool // a number of shares, not an amount of money
}

var metrics = map[string]metricInfo{
	Revenue: {flow: true}, OperatingIncome: {flow: true}, Ebitda: {flow: true}, NetIncome: {flow: true},
	OperatingCashFlow: {flow: true}, Capex: {flow: true}, InterestExpense: {flow: true}, DividendsPaid: {flow: true},
	TotalAssets: {}, Equity: {}, TotalDebt: {}, Cash: {}, Shares: {count: true},
}

// outflows are entered as the positive amount spent or paid.
var outflows = map[string]bool{Capex: true, InterestExpense: true, DividendsPaid: true}

// Metrics lists the known metric names, for help and validation messages.
func Metrics() []string {
	return []string{Revenue, OperatingIncome, Ebitda, NetIncome, OperatingCashFlow, Capex, InterestExpense, DividendsPaid, TotalAssets, Equity, TotalDebt, Cash, Shares}
}

// Datum is one reported figure of one security for one period or date.
type Datum struct {
	Security string `json:"security"`
	Metric   string `json:"metric"`
	// Start is empty for a value at a date. End is the period's last day or the date.
	Start string `json:"start,omitempty"`
	End   string `json:"end"`
	// Currency is empty for a share count.
	Currency money.Currency `json:"currency,omitempty"`
	Value    money.Decimal  `json:"value"`
	// Source says who supplied it ("sec-xbrl", "company-report", "manual"); Ref is
	// the document, accession or page. Both are required: a number with no origin
	// is not accepted.
	Source string `json:"source"`
	Ref    string `json:"ref"`
	// Filed is the day the figure became public, empty when not known.
	Filed string `json:"filed,omitempty"`
	Note  string `json:"note,omitempty"`
}

var securityRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Period classifies the length of a flow's period.
type Period string

const (
	Annual    Period = "annual"
	Quarterly Period = "quarterly"
	Instant   Period = "instant"
)

func day(s string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, s)
	return t, err == nil
}

// Period says what kind of period the datum covers, or why it is not one the
// scorecard knows.
func (d Datum) Period() (Period, error) {
	info, ok := metrics[d.Metric]
	if !ok {
		return "", fmt.Errorf("scorecard: unknown metric %q (%s)", d.Metric, strings.Join(Metrics(), ", "))
	}
	end, ok := day(d.End)
	if !ok {
		return "", fmt.Errorf("scorecard: the end %q must be YYYY-MM-DD", d.End)
	}
	if !info.flow {
		if d.Start != "" {
			return "", fmt.Errorf("scorecard: %s is a value at a date: it has no start", d.Metric)
		}
		return Instant, nil
	}
	start, ok := day(d.Start)
	if !ok {
		return "", fmt.Errorf("scorecard: %s covers a period: the start %q must be YYYY-MM-DD", d.Metric, d.Start)
	}
	days := int(end.Sub(start).Hours()/24) + 1
	switch {
	case days >= 350 && days <= 380:
		return Annual, nil
	case days >= 84 && days <= 98:
		return Quarterly, nil
	}
	return "", fmt.Errorf("scorecard: %s covers %d days: only annual and quarterly periods are accepted, so a transition or cumulative period cannot be mistaken for a year", d.Metric, days)
}

// Validate says why the datum is not acceptable.
func (d Datum) Validate() error {
	if !securityRE.MatchString(d.Security) {
		return fmt.Errorf("scorecard: the security %q must be an identifier", d.Security)
	}
	if _, err := d.Period(); err != nil {
		return err
	}
	info := metrics[d.Metric]
	switch {
	case info.count && d.Currency != "":
		return fmt.Errorf("scorecard: %s is a count of shares: it has no currency", d.Metric)
	case !info.count && !d.Currency.Valid():
		return fmt.Errorf("scorecard: %s needs a known currency, not %q", d.Metric, d.Currency)
	}
	if strings.TrimSpace(d.Source) == "" || strings.TrimSpace(d.Ref) == "" {
		return errors.New("scorecard: a figure needs its source and its reference (document, accession or page): a number with no origin is not accepted")
	}
	if d.Filed != "" {
		f, ok := day(d.Filed)
		if !ok {
			return fmt.Errorf("scorecard: filed %q must be YYYY-MM-DD", d.Filed)
		}
		if end, _ := day(d.End); f.Before(end) {
			return fmt.Errorf("scorecard: filed %s is before the period ends %s", d.Filed, d.End)
		}
	}
	if info.count && d.Value.Sign() <= 0 {
		return errors.New("scorecard: a share count must be positive")
	}
	if outflows[d.Metric] && d.Value.Sign() < 0 {
		return fmt.Errorf("scorecard: %s is entered as a positive amount spent, not as a negative cash flow", d.Metric)
	}
	return nil
}

// Key identifies the figure across revisions: the same security, metric and
// period from the same source.
func (d Datum) Key() string {
	return strings.Join([]string{d.Security, d.Metric, d.Start, d.End, d.Source}, "|")
}

// Hash is the content hash of the datum.
func (d Datum) Hash() string {
	s := strings.Join([]string{d.Security, d.Metric, d.Start, d.End, string(d.Currency), d.Value.String(), d.Source, d.Ref, d.Filed}, "\x1f")
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
