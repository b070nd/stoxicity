package policy

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/money"
)

var now = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// reviewed is the example after the user has read it: no longer an example.
func reviewed(t *testing.T) Policy {
	t.Helper()
	p, err := Example("conservative-eur-1000")
	if err != nil {
		t.Fatal(err)
	}
	p.Example = false
	p.Name = "mine"
	return p
}

func errorPaths(ps []Problem) []string {
	var out []string
	for _, p := range Errors(ps) {
		out = append(out, p.Path)
	}
	return out
}

func TestExampleIsValidButCannotBeActivated(t *testing.T) {
	p, err := Example("conservative-eur-1000")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Example {
		t.Fatal("the example must say it is an example")
	}
	if errs := Errors(Validate(p, now)); len(errs) != 0 {
		t.Fatalf("the example has errors: %v", errs)
	}
	pv, err := MakePreview(p, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Blocked()) != 1 || !strings.Contains(pv.Blocked()[0], "example") || pv.Hash != "" {
		t.Errorf("preview of the example: %+v", pv)
	}
	if _, err := Activate(context.Background(), &memStore{}, p, "anything", true, now); err == nil || !strings.Contains(err.Error(), "example") {
		t.Errorf("activating the example: %v", err)
	}
	if _, err := Example("nope"); err == nil {
		t.Error("an unknown profile")
	}
}

func TestExampleLeavesAccountFactsUnknownAndSaysWhatTheyBlock(t *testing.T) {
	p := reviewed(t)
	un := Unknowns(p)
	byPath := map[string]Unknown{}
	for _, u := range un {
		byPath[u.Path] = u
	}
	for _, path := range []string{"account.tax_residence", "account.commission_schedule", "account.fractional_shares", "account.market_data_entitlements"} {
		u, ok := byPath[path]
		if !ok || len(u.Affects) == 0 {
			t.Errorf("%s: %+v", path, u)
		}
	}
	if _, ok := byPath["finances.investable"]; ok {
		t.Error("the example states the investable amount (as an example), so it is known")
	}
	if !strings.Contains(strings.Join(byPath["account.commission_schedule"].Affects, " "), "affordability") {
		t.Error("the commission schedule decides affordability")
	}
	p.Account.TaxResidence = Is("DE", SourceUser)
	for _, u := range Unknowns(p) {
		if u.Path == "account.tax_residence" {
			t.Error("a stated tax residence is not unknown")
		}
	}
	// Unknown facts never block: the reviewed example validates and previews.
	pv, err := MakePreview(reviewed(t), nil, now)
	if err != nil || len(pv.Blocked()) != 0 || pv.Hash == "" {
		t.Errorf("%+v, %v", pv, err)
	}
}

func TestYAMLRoundTripKeepsTheHash(t *testing.T) {
	p := reviewed(t)
	p.Finances.Contributions = []Contribution{{Amount: eur("100.00 EUR"), Every: "monthly", From: "2026-12-01", Source: SourceUser}}
	p.Account.TaxResidence = Is("DE", SourceUser)
	p.Account.Fractional = Is(true, SourceImported)
	p.Account.Permissions = Is([]string{"stocks", "etf"}, SourceUser)
	b, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	h1, _ := Hash(p)
	h2, _ := Hash(back)
	if h1 != h2 {
		t.Errorf("the hash changed through YAML\n%s", b)
	}
	if !strings.Contains(string(b), "1000.00 EUR") {
		t.Errorf("amounts are written as one scalar:\n%s", b)
	}
}

func TestParseIsStrict(t *testing.T) {
	good, _ := Marshal(reviewed(t))
	for name, in := range map[string]string{
		"empty":         "",
		"unknown key":   string(good) + "max_isuer: 0.1\n",
		"two documents": string(good) + "---\nschema: 1\n",
		"not yaml":      "schema: [",
		"wrong type":    strings.Replace(string(good), "tier: 0", "tier: high", 1),
		"bad amount":    strings.Replace(string(good), "1000.00 EUR", "1000 euros", 1),
		"bad decimal":   strings.Replace(string(good), "cash_reserve: \"0.20\"", "cash_reserve: 20%", 1),
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
	if _, err := Parse(bytes.NewReader(good)); err != nil {
		t.Fatalf("the good file: %v", err)
	}
}

func TestValidationFindsEachContradiction(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Policy)
		path string
	}{
		{"schema", func(p *Policy) { p.Schema = 2 }, "schema"},
		{"no name", func(p *Policy) { p.Name = "" }, "name"},
		{"currency", func(p *Policy) { p.BaseCurrency = "XXX" }, "base_currency"},
		{"tier", func(p *Policy) { p.Tier = 4 }, "tier"},
		{"investable in another currency", func(p *Policy) { p.Finances.Investable = Is(usd("1000.00 USD"), SourceUser) }, "finances.investable.value"},
		{"source without value", func(p *Policy) { p.Account.TaxResidence = Stated[string]{Source: SourceUser} }, "account.tax_residence.value"},
		{"value without source", func(p *Policy) { v := "DE"; p.Account.TaxResidence = Stated[string]{Value: &v} }, "account.tax_residence.source"},
		{"bad source", func(p *Policy) { p.Account.Broker = Is("ibkr", "guess") }, "account.broker.source"},
		{"timezone as residence", func(p *Policy) { p.Account.TaxResidence = Is("Europe/Berlin", SourceUser) }, "account.tax_residence.value"},
		{"reserve of 100%", func(p *Policy) { p.Finances.CashReserve = money.MustDecimal("1") }, "finances.cash_reserve"},
		{"reserve as percent", func(p *Policy) { p.Constraints.MaxIssuer = money.MustDecimal("10") }, "constraints.max_issuer"},
		{"sector below issuer", func(p *Policy) { p.Constraints.MaxSector = money.MustDecimal("0.05") }, "constraints.max_sector"},
		{"event risk above sleeve", func(p *Policy) { p.Constraints.MaxEventTradeRisk = money.MustDecimal("0.01") }, "constraints.max_event_trade_risk"},
		{"event sleeve above room", func(p *Policy) {
			p.Constraints.MaxEventSleeve = money.MustDecimal("0.9")
			p.Constraints.MaxEventTradeRisk = money.MustDecimal("0")
		}, "constraints.max_event_sleeve"},
		{"objective", func(p *Policy) { p.Preferences.Objective = "moon" }, "preferences.objective"},
		{"income without dividends", func(p *Policy) { p.Preferences.Objective = "income"; p.Preferences.DividendPreference = "none" }, "preferences.dividend_preference"},
		{"no instruments", func(p *Policy) { p.Preferences.Instruments = nil }, "preferences.instruments"},
		{"bad instrument", func(p *Policy) { p.Preferences.Instruments = []string{"option"} }, "preferences.instruments"},
		{"market case", func(p *Policy) { p.Preferences.Markets = []string{"us"} }, "preferences.markets"},
		{"duplicate currency", func(p *Policy) { p.Preferences.Currencies = []money.Currency{"EUR", "EUR"} }, "preferences.currencies"},
		{"contribution frequency", func(p *Policy) {
			p.Finances.Contributions = []Contribution{{Amount: eur("10.00 EUR"), Every: "daily", From: "2026-12-01"}}
		}, "finances.contributions[0].every"},
		{"contribution ends before start", func(p *Policy) {
			p.Finances.Contributions = []Contribution{{Amount: eur("10.00 EUR"), Every: "monthly", From: "2026-12-01", Until: "2026-11-01"}}
		}, "finances.contributions[0].until"},
		{"contribution of zero", func(p *Policy) {
			p.Finances.Contributions = []Contribution{{Amount: eur("0.00 EUR"), Every: "monthly", From: "2026-12-01"}}
		}, "finances.contributions[0].amount"},
		{"withdrawal date", func(p *Policy) {
			p.Finances.Withdrawals = []Withdrawal{{Amount: eur("10.00 EUR"), On: "next spring"}}
		}, "finances.withdrawals[0].on"},
		{"goal in the past", func(p *Policy) { p.Goal.TargetDate = Is(Day("2020-01-01"), SourceUser) }, "goal.target_date.value"},
		{"goal not a date", func(p *Policy) { p.Goal.TargetDate = Is(Day("2031-02-30"), SourceUser) }, "goal.target_date.value"},
		{"missing amount", func(p *Policy) { p.Constraints.MinPosition = money.Amount{} }, "constraints.min_position"},
		{"account type", func(p *Policy) { p.Account.AccountType = Is("isa", SourceUser) }, "account.account_type.value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := reviewed(t)
			tt.edit(&p)
			if got := errorPaths(Validate(p, now)); !slices.Contains(got, tt.path) {
				t.Errorf("errors at %v, want one at %s", got, tt.path)
			}
		})
	}
	// A withdrawal after the goal date and a drip with no fractions are warnings.
	p := reviewed(t)
	p.Finances.Withdrawals = []Withdrawal{{Amount: eur("10.00 EUR"), On: "2040-01-01"}}
	p.Preferences.IncomeMode = "drip"
	p.Account.Fractional = Is(false, SourceUser)
	got := Validate(p, now)
	if len(Errors(got)) != 0 {
		t.Errorf("errors: %v", Errors(got))
	}
	var warned int
	for _, pr := range got {
		if pr.Severity == Warning && (strings.Contains(pr.Path, "withdrawals") || pr.Path == "preferences.income_mode") {
			warned++
		}
	}
	if warned != 2 {
		t.Errorf("warnings: %v", got)
	}
}

func usd(s string) money.Amount { return mustAmt(s) }

func mustAmt(s string) money.Amount {
	a, err := money.ParseAmount(s)
	if err != nil {
		panic(err)
	}
	return a
}

func messages(ps []Problem, substr string) []Problem {
	var out []Problem
	for _, p := range ps {
		if strings.Contains(p.Message, substr) {
			out = append(out, p)
		}
	}
	return out
}

func TestFeasibilityNamesTheConstraintThatCannotBeMet(t *testing.T) {
	// The example fits: eight 100 EUR positions plus a fund cap hold the core.
	if f := Feasibility(reviewed(t)); len(f) != 0 {
		t.Errorf("the example is infeasible: %v", f)
	}

	p := reviewed(t)
	p.Constraints.MinPosition = eur("150.00 EUR") // above 10% of 1000
	p.Preferences.Instruments = []string{"stock"}
	f := messages(Feasibility(p), "no single stock can satisfy both")
	if len(f) != 1 || f[0].Path != "constraints.min_position" || !strings.Contains(f[0].Message, "100.00 EUR") {
		t.Errorf("min position above the issuer cap: %v", Feasibility(p))
	}
	if all := messages(Feasibility(p), "could only stay cash"); len(all) != 1 {
		t.Errorf("nothing can be placed: %v", Feasibility(p))
	}

	p = reviewed(t)
	p.Preferences.Instruments = []string{"fund"}
	p.Constraints.MaxFund = money.MustDecimal("0.30")
	got := Feasibility(p)
	if len(messages(got, "at least 4 funds")) != 1 {
		t.Errorf("funds only: %v", got)
	}

	p = reviewed(t)
	p.Constraints.MaxEventSleeve = money.MustDecimal("0.05")
	p.Constraints.MaxEventTradeRisk = money.MustDecimal("0.01")
	p.Tier = 2
	if got := messages(Feasibility(p), "below the minimum position"); len(got) != 1 {
		t.Errorf("an event sleeve of 50 EUR cannot open a 100 EUR position: %v", Feasibility(p))
	}

	p = reviewed(t)
	p.Preferences.Instruments = []string{"stock"}
	p.Constraints.MinPosition = eur("0.00 EUR")
	if got := messages(Feasibility(p), "cannot be bounded"); len(got) != 1 {
		t.Errorf("no minimum position: %v", Feasibility(p))
	}

	// Feasibility never changes the policy and never invents a smaller lot.
	before, _ := Hash(p)
	Feasibility(p)
	if after, _ := Hash(p); after != before {
		t.Error("Feasibility changed the policy")
	}
	// No investable amount: nothing to say yet.
	p = reviewed(t)
	p.Finances.Investable = Stated[money.Amount]{}
	if got := Feasibility(p); got != nil {
		t.Errorf("%v", got)
	}
}

func TestDiffRiskAndEffects(t *testing.T) {
	old := reviewed(t)
	next := reviewed(t)
	if ch, _ := Diff(old, next); len(ch) != 0 {
		t.Errorf("identical policies differ: %v", ch)
	}

	next.Constraints.MaxIssuer = money.MustDecimal("0.15")
	next.Finances.CashReserve = money.MustDecimal("0.10")
	next.Preferences.Markets = append(slices.Clone(next.Preferences.Markets), "JP")
	next.Finances.Investable = Is(eur("1500.00 EUR"), SourceUser)
	next.Preferences.ExcludedSectors = []string{}
	ch, err := Diff(old, next)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]Change{}
	for _, c := range ch {
		paths[c.Path] = c
	}
	if c := paths["constraints.max_issuer"]; c.Old != "0.10" || c.New != "0.15" {
		t.Errorf("max_issuer: %+v", c)
	}
	if c := paths["finances.investable.value"]; c.Old != "1000.00 EUR" || c.New != "1500.00 EUR" {
		t.Errorf("an amount is one leaf: %+v", c)
	}
	if c := paths["finances.investable.source"]; c.Old != "example" || c.New != "user" {
		t.Errorf("source: %+v", c)
	}
	if c := paths["preferences.markets[5]"]; c.Old != "(absent)" || c.New != "JP" {
		t.Errorf("a new list entry: %+v", c)
	}

	risk := RiskIncreases(old, next)
	for _, want := range []string{"max_issuer raised", "cash_reserve lowered", "markets widened by JP"} {
		found := false
		for _, r := range risk {
			found = found || strings.Contains(r, want)
		}
		if !found {
			t.Errorf("risk increases lack %q: %v", want, risk)
		}
	}

	// The reverse direction is safer and needs no acceptance.
	if r := RiskIncreases(next, old); len(r) != 0 {
		t.Errorf("tightening limits is not a risk increase: %v", r)
	}
	tier := reviewed(t)
	tier.Tier = 1
	if r := RiskIncreases(old, tier); len(r) != 1 || !strings.Contains(r[0], "tier raised") {
		t.Errorf("tier: %v", r)
	}
	narrow := reviewed(t)
	narrow.Preferences.ExcludedSectors = []string{"tobacco"}
	wide := reviewed(t)
	if r := RiskIncreases(narrow, wide); len(r) != 1 || !strings.Contains(r[0], "no longer excludes tobacco") {
		t.Errorf("dropping an exclusion: %v", r)
	}

	eff := strings.Join(Effects(ch), " | ")
	for _, want := range []string{"confirmation", "rebuilt", "recomputed"} {
		if !strings.Contains(eff, want) {
			t.Errorf("effects %q lack %q", eff, want)
		}
	}
	rename := reviewed(t)
	rename.Name = "renamed"
	rc, _ := Diff(old, rename)
	if e := Effects(rc); len(e) != 1 || !strings.Contains(e[0], "only the name") {
		t.Errorf("a rename invalidates nothing: %v", e)
	}
	acct := reviewed(t)
	acct.Account.Broker = Is("ibkr", SourceUser)
	ac, _ := Diff(old, acct)
	if e := strings.Join(Effects(ac), "|"); !strings.Contains(e, "confirmation") || strings.Contains(e, "rebuilt") {
		t.Errorf("an account fact recomputes costs but does not rebuild proposals: %s", e)
	}
	if Effects(nil) != nil {
		t.Error("no changes, no effects")
	}
}

// memStore is an in-memory Store with the same rules as the real one.
type memStore struct{ recs []Record }

func (m *memStore) Append(_ context.Context, a Activation) (Record, error) {
	var newest int64
	if n := len(m.recs); n > 0 {
		newest = m.recs[n-1].Epoch
		if a.At.Before(m.recs[n-1].At) {
			return Record{}, errors.New("before the previous activation")
		}
	}
	if newest != a.PrevEpoch {
		return Record{}, ErrStale
	}
	r := Record{Epoch: newest + 1, Hash: a.Hash, At: a.At, Policy: a.Policy, RiskAccepted: a.RiskAccepted, RiskNotes: a.RiskNotes, Changes: a.Changes, Effects: a.Effects}
	m.recs = append(m.recs, r)
	return r, nil
}

func (m *memStore) AsOf(_ context.Context, t time.Time) (Record, bool, error) {
	for i := len(m.recs) - 1; i >= 0; i-- {
		if t.IsZero() || !m.recs[i].At.After(t) {
			return m.recs[i], true, nil
		}
	}
	return Record{}, false, nil
}

func (m *memStore) History(context.Context) ([]Record, error) { return m.recs, nil }

func TestActivationNeedsTheExactConsent(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	p := reviewed(t)
	h, _ := Hash(p)

	if _, err := Activate(ctx, st, p, "", false, now); err == nil {
		t.Error("no consent")
	}
	if _, err := Activate(ctx, st, p, "0000", false, now); err == nil || !strings.Contains(err.Error(), h) {
		t.Errorf("wrong consent should name the hash to give: %v", err)
	}
	rec, err := Activate(ctx, st, p, h, false, now)
	if err != nil || rec.Epoch != 1 || rec.Hash != h {
		t.Fatalf("%+v, %v", rec, err)
	}

	// Activating what is in force changes nothing and records nothing.
	again, err := Activate(ctx, st, p, h, false, now.Add(time.Hour))
	if err != nil || again.Epoch != 1 || len(st.recs) != 1 {
		t.Errorf("repeat: %+v, %v, %d records", again, err, len(st.recs))
	}

	// Consent to one policy cannot activate another.
	other := reviewed(t)
	other.Constraints.MaxIssuer = money.MustDecimal("0.05")
	if _, err := Activate(ctx, st, other, h, false, now.Add(2*time.Hour)); err == nil {
		t.Error("the old hash must not activate a different policy")
	}
	oh, _ := Hash(other)
	rec, err = Activate(ctx, st, other, oh, false, now.Add(2*time.Hour))
	if err != nil || rec.Epoch != 2 || rec.RiskAccepted || len(rec.Changes) == 0 || len(rec.Effects) == 0 {
		t.Fatalf("a tightening: %+v, %v", rec, err)
	}
}

func TestRiskIncreasesNeedAcceptance(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	base := reviewed(t)
	bh, _ := Hash(base)
	if _, err := Activate(ctx, st, base, bh, false, now); err != nil {
		t.Fatal(err)
	}
	riskier := reviewed(t)
	riskier.Constraints.MaxIssuer = money.MustDecimal("0.20")
	rh, _ := Hash(riskier)
	if _, err := Activate(ctx, st, riskier, rh, false, now.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "-accept-risk") || !strings.Contains(err.Error(), "max_issuer") {
		t.Fatalf("risk without acceptance: %v", err)
	}
	if len(st.recs) != 1 {
		t.Fatal("a refused activation recorded something")
	}
	rec, err := Activate(ctx, st, riskier, rh, true, now.Add(time.Hour))
	if err != nil || !rec.RiskAccepted || len(rec.RiskNotes) == 0 {
		t.Errorf("%+v, %v", rec, err)
	}
}

func TestPolicyInForceAtATimeIgnoresTheFuture(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	a := reviewed(t)
	b := reviewed(t)
	b.Constraints.MaxIssuer = money.MustDecimal("0.05")
	ah, _ := Hash(a)
	bh, _ := Hash(b)
	t1, t2 := now, now.Add(48*time.Hour)
	if _, err := Activate(ctx, st, a, ah, false, t1); err != nil {
		t.Fatal(err)
	}
	if _, err := Activate(ctx, st, b, bh, false, t2); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.AsOf(ctx, t1.Add(-time.Second)); ok {
		t.Error("a policy before the first activation")
	}
	r, _, _ := st.AsOf(ctx, t1.Add(time.Hour))
	if r.Hash != ah {
		t.Error("replay at t1+1h must see the first policy, not the one activated later")
	}
	if r, _, _ := st.AsOf(ctx, time.Time{}); r.Hash != bh {
		t.Error("now is the newest")
	}
	if !Stale(1, 2) || Stale(2, 2) {
		t.Error("Stale")
	}
}

func TestTwoPreviewsCannotBothBeActivated(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	a := reviewed(t)
	ah, _ := Hash(a)
	if _, err := Activate(ctx, st, a, ah, false, now); err != nil {
		t.Fatal(err)
	}
	// Two candidates are previewed against epoch 1; the second append is stale.
	b := reviewed(t)
	b.Constraints.MaxSector = money.MustDecimal("0.20")
	c := reviewed(t)
	c.Constraints.MaxSector = money.MustDecimal("0.22")
	cur, _, _ := st.AsOf(ctx, time.Time{})
	pb, _ := MakePreview(b, &cur, now)
	pc, _ := MakePreview(c, &cur, now)
	if _, err := st.Append(ctx, Activation{PrevEpoch: cur.Epoch, Hash: pb.Hash, Policy: b, At: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, Activation{PrevEpoch: cur.Epoch, Hash: pc.Hash, Policy: c, At: now.Add(2 * time.Hour)}); !errors.Is(err, ErrStale) {
		t.Errorf("second activation against the old epoch: %v", err)
	}
}

func TestHashSeesEveryField(t *testing.T) {
	p := reviewed(t)
	h0, _ := Hash(p)
	edits := []func(*Policy){
		func(p *Policy) { p.Tier = 1 },
		func(p *Policy) { p.Goal.AcceptableLoss = money.MustDecimal("0.30") },
		func(p *Policy) { p.Account.Broker = Is("ibkr", SourceUser) },
		func(p *Policy) { p.Preferences.ExcludedIssuers = []string{"XYZ"} },
		func(p *Policy) { p.Finances.Liquidity = "x" },
	}
	for i, e := range edits {
		q := reviewed(t)
		e(&q)
		if h, _ := Hash(q); h == h0 {
			t.Errorf("edit %d did not change the hash", i)
		}
	}
}

// An unset provider cap is not part of the stored policy, so a policy activated
// before the cap existed keeps the hash its confirmations and plans recorded.
func TestProviderCapIsOptionalAndDoesNotChangeOldHashes(t *testing.T) {
	p := reviewed(t)
	b, _ := Canonical(p)
	if strings.Contains(string(b), "max_provider") {
		t.Fatalf("an unset provider cap must not be written: %s", b)
	}
	h0, _ := Hash(p)
	p.Constraints.MaxProvider = money.MustDecimal("0.70")
	if h, _ := Hash(p); h == h0 {
		t.Fatal("setting the provider cap must change the hash")
	}
	if paths := errorPaths(Validate(p, now)); slices.Contains(paths, "constraints.max_provider") {
		t.Fatalf("0.70 is above the fund cap and valid: %v", paths)
	}
	p.Constraints.MaxProvider = money.MustDecimal("0.10")
	p.Constraints.MaxFund = money.MustDecimal("0.60")
	if paths := errorPaths(Validate(p, now)); !slices.Contains(paths, "constraints.max_provider") {
		t.Fatalf("a provider cap below the single-fund cap must be refused: %v", paths)
	}
}
