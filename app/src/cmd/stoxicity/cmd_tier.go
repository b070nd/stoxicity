package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/tier"
)

const (
	recTierConfig   = "tier.config"
	recTierEvidence = "tier.evidence"
	recTierDecision = "tier.decision"
	recTierSignal   = "tier.signal"
)

// cmdTier shows what the current tier lets the program do, what is missing to
// unlock the next one, and what is suspended. A tier is operational; the
// investment limits stay the policy's, and a higher tier loosens none of them.
func cmdTier(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: status | ready | evidence | config | unlock | signal | check")
	}
	switch args[0] {
	case "status":
		return tierStatus(ctx, a, args[1:])
	case "ready":
		return tierReady(ctx, a, args[1:])
	case "evidence":
		return tierEvidence(ctx, a, args[1:])
	case "config":
		return tierConfigCmd(ctx, a, args[1:])
	case "unlock":
		return tierUnlock(ctx, a, args[1:])
	case "signal":
		return tierSignal(ctx, a, args[1:])
	case "check":
		return tierCheck(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (status, ready, evidence, config, unlock, signal, check)", args[0])
}

func tierKey(target int) string { return "T" + strconv.Itoa(target) }

func loadTierConfig(ctx context.Context, a *app) (tier.Config, bool, error) {
	rs, err := a.records()
	if err != nil {
		return tier.Config{}, false, err
	}
	rec, err := rs.Latest(ctx, recTierConfig, "config")
	if errors.Is(err, store.ErrNoRecord) {
		return tier.ExampleConfig(), false, nil
	}
	if err != nil {
		return tier.Config{}, false, err
	}
	var c tier.Config
	if err := json.Unmarshal(rec.Body, &c); err != nil {
		return c, false, fmt.Errorf("the stored tier configuration does not decode: %w", err)
	}
	return c, true, nil
}

func loadEvidence(ctx context.Context, a *app, target int) (map[tier.Item]tier.Evidence, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	out := map[tier.Item]tier.Evidence{}
	for _, it := range tier.Required(target) {
		rec, err := rs.Latest(ctx, recTierEvidence, tierKey(target)+"/"+string(it))
		if errors.Is(err, store.ErrNoRecord) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var e tier.Evidence
		if err := json.Unmarshal(rec.Body, &e); err != nil {
			return nil, fmt.Errorf("evidence %s does not decode: %w", it, err)
		}
		out[it] = e
	}
	return out, nil
}

func loadSignals(ctx context.Context, a *app) ([]tier.Signal, error) {
	rs, err := a.records()
	if err != nil {
		return nil, err
	}
	var out []tier.Signal
	for _, k := range tier.Kinds() {
		rec, err := rs.Latest(ctx, recTierSignal, string(k))
		if errors.Is(err, store.ErrNoRecord) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var s tier.Signal
		if err := json.Unmarshal(rec.Body, &s); err != nil {
			return nil, fmt.Errorf("signal %s does not decode: %w", k, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// policyTier is the tier of the active policy; 0 when none is active, which is
// paper only.
func policyTier(ctx context.Context, a *app) (int, bool, error) {
	db, err := a.store()
	if err != nil {
		return 0, false, err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{})
	if err != nil || !active {
		return 0, false, err
	}
	return rec.Policy.Tier, true, nil
}

// tierGate is what orders are checked against.
func tierGate(ctx context.Context, a *app) (*order.Gate, error) {
	t, _, err := policyTier(ctx, a)
	if err != nil {
		return nil, err
	}
	sig, err := loadSignals(ctx, a)
	if err != nil {
		return nil, err
	}
	return &order.Gate{Tier: t, Suspended: tier.Suspended(sig)}, nil
}

// sessionsObserved counts the weekdays from the first transaction in any book
// to today. It is an approximation of completed market sessions: weekends are
// left out and exchange holidays are not known.
func sessionsObserved(ctx context.Context, a *app) (int, string, error) {
	ps, err := a.portfolios()
	if err != nil {
		return 0, "", err
	}
	books, err := ps.Books(ctx)
	if err != nil {
		return 0, "", err
	}
	var first time.Time
	for _, b := range books {
		l, err := ps.Ledger(ctx, b.ID)
		if err != nil {
			return 0, "", err
		}
		for _, tx := range l.Txs() {
			if first.IsZero() || tx.Event.Before(first) {
				first = tx.Event
			}
		}
	}
	if first.IsZero() {
		return 0, "", nil
	}
	n := 0
	today := a.now().UTC().Truncate(24 * time.Hour)
	for d := first.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1); !d.After(today); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n, first.UTC().Format(time.DateOnly), nil
}

func assessTier(ctx context.Context, a *app, target int) (tier.Readiness, tier.Config, string, error) {
	cfg, _, err := loadTierConfig(ctx, a)
	if err != nil {
		return tier.Readiness{}, cfg, "", err
	}
	ev, err := loadEvidence(ctx, a, target)
	if err != nil {
		return tier.Readiness{}, cfg, "", err
	}
	n, since, err := sessionsObserved(ctx, a)
	if err != nil {
		return tier.Readiness{}, cfg, "", err
	}
	r, err := tier.Assess(target, ev, cfg, n)
	return r, cfg, since, err
}

func tierStatus(ctx context.Context, a *app, args []string) error {
	fs := newFlags("tier status")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	cur, active, err := policyTier(ctx, a)
	if err != nil {
		return err
	}
	sig, err := loadSignals(ctx, a)
	if err != nil {
		return err
	}
	susp := tier.Suspended(sig)
	p := tier.PermissionsOf(cur)
	var next *tier.Readiness
	if cur < 3 {
		r, _, _, err := assessTier(ctx, a, cur+1)
		if err != nil {
			return err
		}
		next = &r
	}
	if *asJSON {
		return printJSON(a, struct {
			Tier        int              `json:"tier"`
			Name        string           `json:"name"`
			PolicyOn    bool             `json:"policy_active"`
			Permissions tier.Permissions `json:"permissions"`
			Suspended   []string         `json:"new_risk_suspended"`
			Next        *tier.Readiness  `json:"next_tier_readiness,omitempty"`
		}{cur, tier.Name(cur), active, p, susp, next})
	}
	src := "the active policy"
	if !active {
		src = "no active policy: paper only"
	}
	fmt.Fprintf(a.stdout, "tier %d: %s (%s)\n", cur, tier.Name(cur), src)
	t := cliTable{Header: []string{"permission", "allowed"}}
	for _, r := range []struct {
		n string
		v bool
	}{{"paper trading and research", p.Paper}, {"a real portfolio managed by hand", p.ManualReal}, {"orders submitted through a broker after confirmation", p.BrokerSubmission}, {"a real event sleeve", p.EventSleeveReal}} {
		c := styled("no", cliDim)
		if r.v {
			c = styled("yes", cliStrong)
		}
		t.Rows = append(t.Rows, []cliCell{plain(r.n), c})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout, "These are operational permissions. The investment limits are the policy's and are the same at every tier.")
	if len(susp) > 0 {
		fmt.Fprintln(a.stdout)
		fmt.Fprintln(a.stdout, styledText("NEW RISK IS SUSPENDED for real and broker books:", cliRed, useColor(a.stdout)))
		for _, s := range susp {
			fmt.Fprintf(a.stdout, "  %s\n", s)
		}
		fmt.Fprintln(a.stdout, "Holdings and open orders are untouched; selling, cancelling and reporting stay possible. Resume with tier signal clear.")
	} else {
		fmt.Fprintln(a.stdout, "new risk is not suspended.")
	}
	if next != nil {
		fmt.Fprintf(a.stdout, "\nto reach tier %d: %s\n", next.Target, readySummary(*next))
	}
	return nil
}

func readySummary(r tier.Readiness) string {
	if r.Ready {
		return "the evidence is complete; tier unlock records your acceptance, then a policy with that tier can be activated"
	}
	return fmt.Sprintf("%d thing(s) missing (tier ready %d lists them)", len(r.Missing), r.Target)
}

func tierReady(ctx context.Context, a *app, args []string) error {
	fs := newFlags("tier ready")
	asJSON := fs.Bool("json", false, "print JSON")
	arg, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	target := 0
	if arg == "" {
		cur, _, err := policyTier(ctx, a)
		if err != nil {
			return err
		}
		target = cur + 1
	} else if target, err = strconv.Atoi(arg); err != nil {
		return usageErrorf("%q is not a tier number (1 to 3)", arg)
	}
	if target < 1 || target > 3 {
		return usageErrorf("tier %d: there is nothing to unlock (1 to 3)", target)
	}
	r, cfg, since, err := assessTier(ctx, a, target)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, r)
	}
	fmt.Fprintf(a.stdout, "readiness for tier %d (%s)\n", target, tier.Name(target))
	t := cliTable{Header: []string{"item", "met", "source", "by", "on", "what it means"}}
	for _, row := range r.Rows {
		met, st := "no", cliRed
		if row.Met {
			met, st = "yes", cliGreen
		}
		why := row.Item.Describe()
		if row.Reason != "" {
			why += " (" + row.Reason + ")"
		}
		t.Rows = append(t.Rows, []cliCell{styled(string(row.Item), cliStrong), styled(met, st), plain(orDash(row.Ref)), plain(orDash(row.By)), plain(orDash(row.At)), plain(why)})
	}
	t.write(a.stdout, useColor(a.stdout))
	label := "illustrative, NOT reviewed by you"
	if cfg.Reviewed {
		label = "reviewed by you"
	}
	obs := "no transaction yet"
	if since != "" {
		obs = "weekdays since " + since + "; weekends left out, exchange holidays not known"
	}
	fmt.Fprintf(a.stdout, "observation: %d of %d completed sessions (%s; %s)\n", r.Sessions, r.Floor, obs, label)
	fmt.Fprintln(a.stdout, "The observation floor is necessary and never sufficient. Profit, a winning streak and time passing do not unlock a tier, and nothing here reads them.")
	if r.Ready {
		fmt.Fprintf(a.stdout, "READY: evidence %s. Next: tier unlock %d -by NAME -accept-risk\n", r.Hash[:12], target)
		return nil
	}
	fmt.Fprintln(a.stdout, "NOT READY:")
	for _, m := range r.Missing {
		fmt.Fprintf(a.stdout, "  %s\n", m)
	}
	return nil
}

func tierEvidence(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: set | show")
	}
	switch args[0] {
	case "set":
		fs := newFlags("tier evidence set")
		target := fs.Int("target", 0, "the tier the evidence is for (1 to 3)")
		item := fs.String("item", "", "config_complete, records_reconciled, no_broker_discrepancy, failure_scenarios, evaluation_adequate or tier3_review")
		met := fs.String("met", "yes", "yes or no")
		ref := fs.String("ref", "", "where the proof is (required)")
		note := fs.String("note", "", "a note")
		by := fs.String("by", "", "who attests it")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 || *target == 0 || *item == "" {
			return usageErrorf("usage: tier evidence set -target N -item ITEM -ref SOURCE -by NAME [-met yes|no] [-note TEXT]")
		}
		e := tier.Evidence{Target: *target, Item: tier.Item(*item), Met: yes(*met, true), Ref: *ref, Note: *note, By: *by, At: a.now().UTC()}
		if err := e.Validate(); err != nil {
			return usageErrorf("%v", err)
		}
		rs, err := a.records()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(e)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recTierEvidence, Key: tierKey(e.Target) + "/" + string(e.Item), Body: raw}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "recorded for tier %d: %s %s, source %q, by %s. Earlier statements stay in the history.\n", e.Target, e.Item, map[bool]string{true: "met", false: "NOT met"}[e.Met], e.Ref, e.By)
		return nil
	case "show":
		fs := newFlags("tier evidence show")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 {
			return usageErrorf("unexpected argument %q", rest[0])
		}
		rs, err := a.records()
		if err != nil {
			return err
		}
		all, err := rs.All(ctx, recTierEvidence, "")
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Fprintln(a.stdout, "no readiness evidence is recorded")
			return nil
		}
		t := cliTable{Header: []string{"tier", "item", "met", "source", "by", "on", "note"}}
		for _, rec := range all {
			var e tier.Evidence
			if json.Unmarshal(rec.Body, &e) != nil {
				continue
			}
			met, st := "yes", cliGreen
			if !e.Met {
				met, st = "no", cliRed
			}
			t.Rows = append(t.Rows, []cliCell{plain(strconv.Itoa(e.Target)), styled(string(e.Item), cliStrong), styled(met, st), plain(e.Ref), plain(e.By), plain(e.At.UTC().Format(time.DateOnly)), plain(orDash(e.Note))})
		}
		t.write(a.stdout, useColor(a.stdout))
		return nil
	}
	return usageErrorf("unknown subcommand %q (set, show)", args[0])
}

func tierConfigCmd(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | set | review | show")
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	switch args[0] {
	case "example":
		return writeJSON(a.stdout, tier.ExampleConfig())
	case "show":
		c, stored, err := loadTierConfig(ctx, a)
		if err != nil {
			return err
		}
		if !stored {
			fmt.Fprintln(a.stdout, "# nothing stored: the example below applies and is NOT reviewed")
		}
		return writeJSON(a.stdout, c)
	case "set":
		fs := newFlags("tier config set")
		file := fs.String("file", "", "the configuration as JSON (tier config example prints one)")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 || *file == "" {
			return usageErrorf("usage: tier config set -file FILE")
		}
		var c tier.Config
		if err := readJSONFile(*file, &c); err != nil {
			return err
		}
		c.Reviewed = false
		if err := c.Validate(); err != nil {
			return err
		}
		raw, _ := json.Marshal(c)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recTierConfig, Key: "config", Body: raw}); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, "stored; the thresholds are NOT reviewed until you run tier config review. A tier cannot be unlocked on thresholds you have not read.")
		return nil
	case "review":
		c, stored, err := loadTierConfig(ctx, a)
		if err != nil {
			return err
		}
		_ = stored
		c.Reviewed = true
		raw, _ := json.Marshal(c)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recTierConfig, Key: "config", Body: raw}); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, "the tier thresholds are marked as reviewed by you.")
		return nil
	}
	return usageErrorf("unknown subcommand %q (example, set, review, show)", args[0])
}

func tierUnlock(ctx context.Context, a *app, args []string) error {
	fs := newFlags("tier unlock")
	by := fs.String("by", "", "who accepts the risk")
	accept := fs.Bool("accept-risk", false, "I have read the risk text and accept it")
	note := fs.String("note", "", "a note")
	arg, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	target, convErr := strconv.Atoi(arg)
	if arg == "" || convErr != nil || target < 1 || target > 3 || *by == "" {
		return usageErrorf("usage: tier unlock TIER -by NAME -accept-risk [-note TEXT]")
	}
	r, _, _, err := assessTier(ctx, a, target)
	if err != nil {
		return err
	}
	if !r.Ready {
		return fmt.Errorf("tier %d is not ready: %s", target, strings.Join(r.Missing, "; "))
	}
	text := tier.RiskText(target)
	if !*accept {
		fmt.Fprintln(a.stdout, text)
		return usageErrorf("read the text above and pass -accept-risk to record that you accept it")
	}
	d := tier.Decision{Target: target, EvidenceHash: r.Hash, By: *by, At: a.now().UTC(), RiskText: text, Note: *note}
	rs, err := a.records()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(d)
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recTierDecision, Key: tierKey(target), Body: raw}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "recorded: %s accepted tier %d on %s, on evidence %s.\n%s\n", *by, target, d.At.Format(time.DateOnly), r.Hash[:12], text)
	fmt.Fprintln(a.stdout, "Nothing has changed yet. The tier is the tier of the active policy: write a policy with this tier, preview it, and activate it (policy preview, policy activate -accept-risk). Plans that depend on the old policy go stale and are regenerated.")
	return nil
}

// requireTierDecision refuses a policy that raises the tier without readiness
// and a current decision of the user.
func requireTierDecision(ctx context.Context, a *app, from, to int) error {
	if to <= from {
		return nil
	}
	r, cfg, _, err := assessTier(ctx, a, to)
	if err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	rec, err := rs.Latest(ctx, recTierDecision, tierKey(to))
	if errors.Is(err, store.ErrNoRecord) {
		if !r.Ready {
			return fmt.Errorf("raising the tier to %d needs readiness evidence and your recorded acceptance; not ready: %s (tier ready %d)", to, strings.Join(r.Missing, "; "), to)
		}
		return fmt.Errorf("raising the tier to %d needs your recorded acceptance of its risk: tier unlock %d -by NAME -accept-risk", to, to)
	}
	if err != nil {
		return err
	}
	var d tier.Decision
	if err := json.Unmarshal(rec.Body, &d); err != nil {
		return err
	}
	if err := d.Allows(r, cfg, a.now()); err != nil {
		return fmt.Errorf("raising the tier to %d: %w", to, err)
	}
	return nil
}

func tierSignal(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: set | clear | show")
	}
	switch args[0] {
	case "show":
		sig, err := loadSignals(ctx, a)
		if err != nil {
			return err
		}
		if len(sig) == 0 {
			fmt.Fprintln(a.stdout, "no health signal was ever recorded: new risk is not suspended")
			return nil
		}
		t := cliTable{Header: []string{"signal", "state", "since", "by", "reason"}}
		for _, s := range sig {
			st, style := "cleared", cliGreen
			if s.Active {
				st, style = "ACTIVE", cliRed
			}
			t.Rows = append(t.Rows, []cliCell{styled(string(s.Kind), cliStrong), styled(st, style), plain(s.At.UTC().Format(time.RFC3339)), plain(s.By), plain(s.Reason)})
		}
		t.write(a.stdout, useColor(a.stdout))
		return nil
	case "set", "clear":
		fs := newFlags("tier signal " + args[0])
		reason := fs.String("reason", "", "why")
		by := fs.String("by", "", "who")
		kind, err := parseBookFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if !tier.Kind(kind).Valid() || *reason == "" || *by == "" {
			return usageErrorf("usage: tier signal %s KIND -reason TEXT -by NAME (kinds: %s)", args[0], kindList())
		}
		if err := recordSignal(ctx, a, tier.Signal{Kind: tier.Kind(kind), Active: args[0] == "set", Reason: *reason, By: *by, At: a.now().UTC()}); err != nil {
			return err
		}
		if args[0] == "clear" {
			fmt.Fprintf(a.stdout, "%s cleared by %s. Confirmations that were revoked stay revoked: confirm them again.\n", kind, *by)
			return nil
		}
		n, err := revokeNewRisk(ctx, a, kind+": "+*reason)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "new risk is suspended (%s: %s). %d unsent confirmation(s) revoked. Holdings, open orders and protective orders are untouched; selling, cancelling and reporting stay possible.\n", kind, *reason, n)
		return nil
	}
	return usageErrorf("unknown subcommand %q (set, clear, show)", args[0])
}

func kindList() string {
	var ks []string
	for _, k := range tier.Kinds() {
		ks = append(ks, string(k))
	}
	return strings.Join(ks, ", ")
}

func recordSignal(ctx context.Context, a *app, s tier.Signal) error {
	rs, err := a.records()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(s)
	_, _, err = rs.Append(ctx, store.RecordInput{Kind: recTierSignal, Key: string(s.Kind), Body: raw})
	return err
}

// revokeNewRisk withdraws the confirmations of buys that were confirmed but not
// sent, in every active book that reaches a broker. It returns how many.
func revokeNewRisk(ctx context.Context, a *app, reason string) (int, error) {
	ps, err := a.portfolios()
	if err != nil {
		return 0, err
	}
	books, err := ps.Books(ctx)
	if err != nil {
		return 0, err
	}
	svc, err := orderService(ctx, a)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range books {
		if b.Status != portfolio.Active || b.Env == portfolio.PaperLocal {
			continue
		}
		before, err := confirmedUnsent(ctx, svc, b)
		if err != nil {
			return n, err
		}
		if before == 0 {
			continue
		}
		if err := svc.RevokeUnsent(ctx, b.ID, reason); err != nil {
			return n, err
		}
		n += before
	}
	return n, nil
}

func confirmedUnsent(ctx context.Context, svc order.Service, b portfolio.Book) (int, error) {
	vs, err := svc.Views(ctx, b.Account)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, v := range vs {
		if v.Terms.Book == b.ID && v.State == order.Confirmed && !v.Sent() && v.Terms.Side == order.Buy {
			n++
		}
	}
	return n, nil
}

// tierCheck computes the largest fall of the account's value from an equity
// series you supply and suspends new risk when it reaches the policy's drawdown
// threshold. It can only suspend: resuming is your explicit act.
func tierCheck(ctx context.Context, a *app, args []string) error {
	fs := newFlags("tier check")
	file := fs.String("equity", "", "CSV of day,equity: the value of the account on each day, in the base currency")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("usage: tier check -equity FILE.csv (columns: day, equity)")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, time.Time{})
	if err != nil {
		return err
	}
	if !active {
		return errors.New("no policy is active: the drawdown threshold is the policy's")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	rows, err := csvtable.Read(f, []string{"day", "equity"}, nil)
	if err != nil {
		return err
	}
	type pt struct {
		day string
		v   int64
	}
	var pts []pt
	for _, r := range rows {
		if _, err := time.Parse(time.DateOnly, r["day"]); err != nil {
			return fmt.Errorf("line %s: day %q is not YYYY-MM-DD", r["_line"], r["day"])
		}
		d, err := money.ParseDecimal(r["equity"])
		if err != nil {
			return fmt.Errorf("line %s: equity %q: %w", r["_line"], r["equity"], err)
		}
		am, err := money.NewAmount(d, rec.Policy.BaseCurrency, money.Exact)
		if err != nil {
			return fmt.Errorf("line %s: equity %q has more digits than %s: %w", r["_line"], r["equity"], rec.Policy.BaseCurrency, err)
		}
		pts = append(pts, pt{r["day"], am.Minor()})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].day < pts[j].day })
	vals := make([]int64, len(pts))
	for i, p := range pts {
		if i > 0 && pts[i-1].day == p.day {
			return fmt.Errorf("two values for %s", p.day)
		}
		vals[i] = p.v
	}
	fall, peak := tier.MaxDrawdown(vals)
	threshold := rec.Policy.Constraints.DrawdownReview
	breach, err := tier.Breaches(fall, peak, threshold)
	if err != nil {
		return err
	}
	frac := "none"
	if peak > 0 {
		frac = pcOf(fall, peak)
	}
	fmt.Fprintf(a.stdout, "largest fall in %d days of values: %s (%s of %s); the policy reviews at %s\n", len(vals), frac, minorText(fall, rec.Policy.BaseCurrency), minorText(peak, rec.Policy.BaseCurrency), threshold)
	if !breach {
		fmt.Fprintln(a.stdout, "no suspension. Nothing was changed.")
		return nil
	}
	reason := fmt.Sprintf("a fall of %s from a peak of %s reaches the policy threshold %s", minorText(fall, rec.Policy.BaseCurrency), minorText(peak, rec.Policy.BaseCurrency), threshold)
	if err := recordSignal(ctx, a, tier.Signal{Kind: tier.Drawdown, Active: true, Reason: reason, By: "tier check", At: a.now().UTC()}); err != nil {
		return err
	}
	n, err := revokeNewRisk(ctx, a, "drawdown: "+reason)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "NEW RISK IS SUSPENDED: %s. %d unsent confirmation(s) revoked. Holdings, open orders and protective orders are untouched. The policy calls for a review; resume with tier signal clear drawdown after it.\n", reason, n)
	return nil
}
