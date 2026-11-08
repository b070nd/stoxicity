package strategy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/b070nd/stoxicity/app/src/money"
)

// CoreID names the starter core strategy. Its numbers are unvalidated initial
// research settings, all editable, inactive until a person has reviewed them.
const CoreID = "quality-income-core-v1"

// FundID names the starter broad-fund template.
const FundID = "diversified-core-fund-v1"

// Config holds every threshold of the core strategy. It is stored as a versioned
// record; the hash of its canonical form goes into every proposal.
type Config struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	// Reviewed says a person read these values and accepted them as the settings
	// to use. An unreviewed configuration can propose nothing stronger than WATCH.
	Reviewed bool `json:"reviewed"`

	MinYears            int           `json:"min_years"`              // comparable completed fiscal years
	MinFCFPositiveYears int           `json:"min_fcf_positive_years"` // of the last three
	MaxNetDebtToEBITDA  money.Decimal `json:"max_net_debt_to_ebitda"`
	MinInterestCover    money.Decimal `json:"min_interest_cover"`
	MaxPayoutFCF        money.Decimal `json:"max_payout_of_fcf"`
	MaxPayoutEarnings   money.Decimal `json:"max_payout_of_earnings"`
	MinNetMargin        money.Decimal `json:"min_net_margin"`
	MinROE              money.Decimal `json:"min_roe"`
	MinPayoutHistory    int           `json:"min_payout_history"` // declared ordinary payouts in the last five years

	MinScore       int           `json:"min_score"`
	MarginOfSafety money.Decimal `json:"margin_of_safety"`
	// EntryScenario is "base" or "bear": the scenario the margin of safety is taken from.
	EntryScenario   string `json:"entry_scenario"`
	MaxPriceAgeDays int    `json:"max_price_age_days"`
	ReviewMonths    int    `json:"review_months"`
}

// DefaultConfig is the starter configuration, not reviewed.
func DefaultConfig() Config {
	d := money.MustDecimal
	return Config{
		ID: CoreID, Version: 1, Reviewed: false,
		MinYears: 3, MinFCFPositiveYears: 2,
		MaxNetDebtToEBITDA: d("3"), MinInterestCover: d("4"),
		MaxPayoutFCF: d("0.80"), MaxPayoutEarnings: d("0.70"),
		MinNetMargin: d("0.05"), MinROE: d("0.10"), MinPayoutHistory: 4,
		MinScore: 70, MarginOfSafety: d("0.20"), EntryScenario: "base",
		MaxPriceAgeDays: 7, ReviewMonths: 3,
	}
}

// Validate says why the configuration cannot be used.
func (c Config) Validate() error {
	switch {
	case c.ID != CoreID:
		return fmt.Errorf("strategy: unknown strategy %q (%s)", c.ID, CoreID)
	case c.Version < 1:
		return errors.New("strategy: the version must be 1 or more")
	case c.MinYears < 3 || c.MinYears > 10:
		return errors.New("strategy: at least three, at most ten comparable fiscal years")
	case c.MinFCFPositiveYears < 1 || c.MinFCFPositiveYears > 3:
		return errors.New("strategy: positive free cash flow years must be 1 to 3 of the last three")
	case c.MaxNetDebtToEBITDA.Sign() <= 0 || c.MinInterestCover.Sign() <= 0:
		return errors.New("strategy: the leverage and cover limits must be positive")
	case c.MaxPayoutFCF.Sign() <= 0 || c.MaxPayoutEarnings.Sign() <= 0:
		return errors.New("strategy: the payout ceilings must be positive")
	case c.MinPayoutHistory < 1 || c.MinPayoutHistory > 20:
		return errors.New("strategy: the payout history must be 1 to 20 payouts")
	case c.MinScore < 1 || c.MinScore > 100:
		return errors.New("strategy: the minimum score must be 1 to 100")
	case c.MarginOfSafety.Sign() < 0 || c.MarginOfSafety.Cmp(money.MustDecimal("0.9")) > 0:
		return errors.New("strategy: the margin of safety must be 0 to 90%")
	case c.EntryScenario != "base" && c.EntryScenario != "bear":
		return errors.New("strategy: the entry scenario must be base or bear")
	case c.MaxPriceAgeDays < 1 || c.MaxPriceAgeDays > 60:
		return errors.New("strategy: the price may be 1 to 60 days old")
	case c.ReviewMonths < 1 || c.ReviewMonths > 12:
		return errors.New("strategy: the review interval must be 1 to 12 months")
	}
	return nil
}

// Hash identifies the configuration.
func (c Config) Hash() string { return hashJSON(c) }

func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Hash identifies the assumptions.
func (a Assumptions) Hash() string { return hashJSON(a) }
