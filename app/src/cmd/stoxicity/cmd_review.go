package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/money"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/portfolio"
	"github.com/b070nd/stoxicity/app/src/review"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/strategy"
)

const (
	recReviewConfig = "review.config"
	recThesis       = "review.thesis"
	recInstruction  = "review.instruction"
	recReviewLog    = "review.log"
)

// cmdReview looks at what you own: hold, reduce or sell, with the rule that
// decided and the conditions against it. It proposes quantities from free
// shares only and never places anything.
func cmdReview(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: run | config | thesis | instruct | done")
	}
	switch args[0] {
	case "run":
		return reviewRun(ctx, a, args[1:])
	case "config":
		return reviewConfig(ctx, a, args[1:])
	case "thesis":
		return reviewThesis(ctx, a, args[1:])
	case "instruct":
		return reviewInstruct(ctx, a, args[1:])
	case "done":
		return reviewDone(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (run, config, thesis, instruct, done)", args[0])
}

func loadReviewConfig(ctx context.Context, rs store.Records) (review.Config, bool, error) {
	r, err := rs.Latest(ctx, recReviewConfig, review.ID)
	if errors.Is(err, store.ErrNoRecord) {
		return review.DefaultConfig(), false, nil
	}
	if err != nil {
		return review.Config{}, false, err
	}
	var c review.Config
	if err := json.Unmarshal(r.Body, &c); err != nil {
		return review.Config{}, false, err
	}
	return c, true, nil
}

func reviewConfig(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: config example | set -file F | review | show")
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	switch args[0] {
	case "example":
		return printJSON(a, review.DefaultConfig())
	case "set":
		fs := newFlags("review config set")
		file := fs.String("file", "", "JSON configuration (review config example shows it)")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 || *file == "" {
			return usageErrorf("usage: review config set -file FILE")
		}
		var c review.Config
		if err := readJSONFile(*file, &c); err != nil {
			return err
		}
		if err := c.Validate(); err != nil {
			return err
		}
		c.Reviewed = false
		body, _ := json.Marshal(c)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recReviewConfig, Key: c.ID, Body: body}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "stored %s version %d (%s). It is NOT reviewed: until you run review config review, the thresholds (goal window, valuation, event limit) require a review and propose no sale.\n", c.ID, c.Version, c.Hash()[:12])
		return nil
	case "review":
		c, stored, err := loadReviewConfig(ctx, rs)
		if err != nil {
			return err
		}
		if !stored {
			return errors.New("no configuration is stored: run review config set first, so what you review is what is used")
		}
		c.Reviewed = true
		body, _ := json.Marshal(c)
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recReviewConfig, Key: c.ID, Body: body}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s version %d (%s) is marked as reviewed by you. These thresholds are unvalidated starting points.\n", c.ID, c.Version, c.Hash()[:12])
		return nil
	case "show":
		c, stored, err := loadReviewConfig(ctx, rs)
		if err != nil {
			return err
		}
		if !stored {
			fmt.Fprintln(a.stdout, "no configuration stored: the starter configuration is used, unreviewed")
		}
		return printJSON(a, c)
	}
	return usageErrorf("unknown subcommand %q", args[0])
}

type thesisRec struct {
	Security          string    `json:"security"`
	Status            string    `json:"status"`
	DependsOnDividend bool      `json:"depends_on_dividend"`
	Note              string    `json:"note"`
	Source            string    `json:"source"`
	Ref               string    `json:"ref"`
	By                string    `json:"by"`
	Day               string    `json:"day"`
	At                time.Time `json:"at"`
}

type instructionRec struct {
	Security string `json:"security"`
	Action   string `json:"action"`
	Qty      string `json:"qty,omitempty"`
	All      bool   `json:"all,omitempty"`
	Why      string `json:"why"`
	Note     string `json:"note,omitempty"`
	By       string `json:"by"`
	Day      string `json:"day"`
	Cleared  bool   `json:"cleared,omitempty"`
}

type logRec struct {
	Security string `json:"security"`
	Outcome  string `json:"outcome"`
	Note     string `json:"note"`
	By       string `json:"by"`
	Day      string `json:"day"`
}

func today(a *app, fl string) (string, error) {
	day := a.now().UTC().Format(time.DateOnly)
	if fl != "" {
		day = fl
	}
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return "", usageErrorf("the day must be YYYY-MM-DD")
	}
	return day, nil
}

func reviewThesis(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: thesis set -security S -status intact|under_review|broken ... | show [-security S]")
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	switch args[0] {
	case "set":
		fs := newFlags("review thesis set")
		sec := fs.String("security", "", "the security ID")
		status := fs.String("status", "", "intact, under_review or broken")
		dep := fs.Bool("depends-on-dividend", false, "your thesis counted on the dividend")
		note := fs.String("note", "", "why you hold this view")
		source := fs.String("source", "", "where the evidence is")
		ref := fs.String("ref", "", "the document or date")
		by := fs.String("by", "", "who is recording this")
		dayFl := fs.String("day", "", "the day, default today")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 || *sec == "" || *by == "" || strings.TrimSpace(*note) == "" || *source == "" || *ref == "" {
			return usageErrorf("usage: review thesis set -security S -status intact|under_review|broken -note TEXT -source S -ref R -by NAME [-depends-on-dividend] [-day D]")
		}
		if *status != "intact" && *status != "under_review" && *status != "broken" {
			return usageErrorf("-status must be intact, under_review or broken")
		}
		secs, err := securitySet(ctx, a)
		if err != nil {
			return err
		}
		if err := knownSecurity(secs, *sec); err != nil {
			return err
		}
		day, err := today(a, *dayFl)
		if err != nil {
			return err
		}
		body, _ := json.Marshal(thesisRec{*sec, *status, *dep, *note, *source, *ref, *by, day, a.now().UTC()})
		if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recThesis, Key: *sec, Body: body}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "thesis of %s recorded as %s on %s by %s. Only a thesis you record as broken proposes a sale; the evidence stays yours.\n", *sec, *status, day, *by)
		return nil
	case "show":
		fs := newFlags("review thesis show")
		sec := fs.String("security", "", "the security ID; default all")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 {
			return usageErrorf("unexpected argument %q", rest[0])
		}
		prefix := ""
		if *sec != "" {
			prefix = *sec
		}
		recs, err := rs.Current(ctx, recThesis, prefix)
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			fmt.Fprintln(a.stdout, "no thesis is recorded")
			return nil
		}
		t := cliTable{Header: []string{"security", "status", "dividend", "day", "by", "note", "source"}}
		for _, r := range recs {
			var v thesisRec
			if err := json.Unmarshal(r.Body, &v); err != nil {
				return err
			}
			dv := "no"
			if v.DependsOnDividend {
				dv = "yes"
			}
			st := cliGreen
			if v.Status != "intact" {
				st = cliYellow
			}
			t.Rows = append(t.Rows, []cliCell{styled(v.Security, cliStrong), styled(v.Status, st), plain(dv), plain(v.Day), plain(v.By), plain(v.Note), plain(v.Source + " " + v.Ref)})
		}
		t.write(a.stdout, useColor(a.stdout))
		return nil
	}
	return usageErrorf("unknown subcommand %q", args[0])
}

func reviewInstruct(ctx context.Context, a *app, args []string) error {
	fs := newFlags("review instruct")
	sec := fs.String("security", "", "the security ID")
	action := fs.String("action", "", "reduce or sell")
	qty := fs.String("qty", "", "shares to sell")
	all := fs.Bool("all", false, "all the free shares")
	why := fs.String("why", "", "withdrawal, liquidity or instruction")
	note := fs.String("note", "", "a note")
	by := fs.String("by", "", "who is instructing")
	clear := fs.Bool("clear", false, "withdraw the instruction (after you have acted on it)")
	dayFl := fs.String("day", "", "the day, default today")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *sec == "" || *by == "" {
		return usageErrorf("usage: review instruct -security S -action reduce|sell (-qty Q | -all) -why withdrawal|liquidity|instruction -by NAME [-note T] | -clear -by NAME")
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	if err := knownSecurity(secs, *sec); err != nil {
		return err
	}
	day, err := today(a, *dayFl)
	if err != nil {
		return err
	}
	rec := instructionRec{Security: *sec, By: *by, Day: day, Note: *note}
	if *clear {
		rec.Cleared = true
	} else {
		if *action != "reduce" && *action != "sell" {
			return usageErrorf("-action must be reduce or sell")
		}
		if *why != "withdrawal" && *why != "liquidity" && *why != "instruction" {
			return usageErrorf("-why must be withdrawal, liquidity or instruction")
		}
		if *all == (*qty != "") {
			return usageErrorf("say how much: -qty Q or -all")
		}
		rec.Action, rec.Why, rec.All = *action, *why, *all
		if *qty != "" {
			q, err := money.ParseDecimal(*qty)
			if err != nil || q.Sign() <= 0 {
				return usageErrorf("-qty must be a positive quantity")
			}
			rec.Qty = q.String()
		}
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(rec)
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recInstruction, Key: *sec, Body: body}); err != nil {
		return err
	}
	if *clear {
		fmt.Fprintf(a.stdout, "the instruction on %s is withdrawn\n", *sec)
		return nil
	}
	fmt.Fprintf(a.stdout, "instruction recorded: %s %s of %s because of your %s. It stays until you clear it; recording it sells nothing.\n", *action, map[bool]string{true: "all the free shares", false: *qty}[*all], *sec, *why)
	return nil
}

func reviewDone(ctx context.Context, a *app, args []string) error {
	fs := newFlags("review done")
	sec := fs.String("security", "", "the security ID")
	outcome := fs.String("outcome", "", "held, reduced, sold or noted")
	note := fs.String("note", "", "what you decided and why")
	by := fs.String("by", "", "who reviewed")
	dayFl := fs.String("day", "", "the day, default today")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *sec == "" || *by == "" || strings.TrimSpace(*note) == "" {
		return usageErrorf("usage: review done -security S -outcome held|reduced|sold|noted -note TEXT -by NAME [-day D]")
	}
	switch *outcome {
	case "held", "reduced", "sold", "noted":
	default:
		return usageErrorf("-outcome must be held, reduced, sold or noted")
	}
	secs, err := securitySet(ctx, a)
	if err != nil {
		return err
	}
	if err := knownSecurity(secs, *sec); err != nil {
		return err
	}
	day, err := today(a, *dayFl)
	if err != nil {
		return err
	}
	rs, err := a.records()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(logRec{*sec, *outcome, *note, *by, day})
	if _, _, err := rs.Append(ctx, store.RecordInput{Kind: recReviewLog, Key: *sec, Body: body}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "review of %s recorded on %s: %s. A review date is not a sale date; this only moves the next review.\n", *sec, day, *outcome)
	return nil
}

// decisionsHash identifies the recorded decisions a review read.
func decisionsHash(ctx context.Context, rs store.Records, sec string, upTo time.Time) (string, error) {
	h := sha256.New()
	for _, kind := range []string{recThesis, recInstruction, recReviewLog} {
		recs, err := rs.History(ctx, kind, sec)
		if err != nil {
			return "", err
		}
		for _, r := range recs {
			if !upTo.IsZero() && r.At.After(upTo) {
				continue
			}
			h.Write([]byte(kind + ":" + r.Hash + "\n"))
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type holdingReview struct {
	Position review.Position
	Name     string
	Review   review.Review
	Manifest plan.Manifest
	Price    string
	Base     money.Currency
	Held     string
	Free     string
	Reviewed bool
}

// reviewHoldings evaluates every position in a book.
func reviewHoldings(ctx context.Context, a *app, bookRef, pricesFile, ratesFlag, day string, pin time.Time) ([]holdingReview, string, error) {
	dayT, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return nil, "", usageErrorf("-as-of must be YYYY-MM-DD")
	}
	known := dayT.Add(24*time.Hour - time.Nanosecond)
	db, err := a.store()
	if err != nil {
		return nil, "", err
	}
	if _, err := a.requireTenant(); err != nil {
		return nil, "", err
	}
	rec, active, err := db.Policies(a.tenantID).AsOf(ctx, pin)
	if err != nil {
		return nil, "", err
	}
	if !active {
		return nil, "", errors.New("no investment policy is active: the review needs your limits and goal date (stoxicity policy example / check / activate)")
	}
	pol := rec.Policy
	base := pol.BaseCurrency
	rt, err := parseRates(ratesFlag, base)
	if err != nil {
		return nil, "", err
	}
	ps, err := a.portfolios()
	if err != nil {
		return nil, "", err
	}
	led, err := ledgerAt(ctx, ps, bookRef, pin)
	if err != nil {
		return nil, "", err
	}
	proj := led.Projection()
	rs, err := a.records()
	if err != nil {
		return nil, "", err
	}
	rcfg, _, err := loadReviewConfig(ctx, rs)
	if err != nil {
		return nil, "", err
	}
	scfg, _, err := loadConfig(ctx, rs)
	if err != nil {
		return nil, "", err
	}
	reg, err := loadRegistry(ctx, a)
	if err != nil {
		return nil, "", err
	}
	names := securityNames(reg)
	issuers := map[string]instrument.Issuer{}
	for _, is := range reg.Set().Issuers {
		issuers[is.ID] = is
	}

	type pr struct {
		price *strategy.Price
		row   map[string]string
	}
	prices := map[string]pr{}
	if pricesFile != "" {
		f, err := os.Open(pricesFile)
		if err != nil {
			return nil, "", err
		}
		rows, err := csvtable.Read(f, []string{"security"}, []string{"mic", "ticker", "sector", "price", "price_day", "fractional", "step", "min_qty", "tradable", "facts"})
		f.Close()
		if err != nil {
			return nil, "", err
		}
		for _, row := range rows {
			sec, ok := names[row["security"]]
			if !ok {
				return nil, "", fmt.Errorf("line %s: %q is not in the instrument register", row["_line"], row["security"])
			}
			var p *strategy.Price
			switch {
			case row["price"] != "":
				am, err := money.ParseAmount(row["price"])
				if err != nil {
					return nil, "", fmt.Errorf("line %s: price: %w", row["_line"], err)
				}
				v, _ := money.ParseDecimal(strings.Fields(row["price"])[0])
				if row["price_day"] == "" {
					return nil, "", fmt.Errorf("line %s: a supplied price needs price_day", row["_line"])
				}
				p = &strategy.Price{Value: v, Currency: am.Currency(), Day: row["price_day"], Source: "supplied"}
			case row["mic"] != "" && row["ticker"] != "":
				p, _, err = storedPrice(ctx, a, db, row["mic"], row["ticker"], day, known, scfg.MaxPriceAgeDays)
				if err != nil {
					return nil, "", err
				}
			}
			prices[sec.ID] = pr{p, row}
		}
	}

	// values, equity and exposures
	positions := proj.Positions()
	type held struct {
		pos   review.Position
		price *strategy.Price
		sec   instrument.Security
		why   []string
	}
	var hs []held
	equity := proj.Cash(base)
	otherCash, otherNotes := foreignCash(proj, base, rt)
	equity += otherCash
	unknown := 0
	byIssuer := map[string]int64{}
	byFund := map[string]int64{}
	byProvider := map[string]int64{}
	for _, p := range positions {
		sec := names[p.Security]
		free, err := p.Qty.Sub(proj.ReservedShares(p.Security, ""))
		if err != nil {
			return nil, "", err
		}
		x := held{sec: sec, price: prices[p.Security].price}
		x.pos = review.Position{Security: p.Security, Name: sec.Name, Sleeve: "core", Qty: p.Qty, Free: free, OpenedDay: openedDay(led, p.Security)}
		if prices[p.Security].row != nil {
			x.pos.Fractional = yes(prices[p.Security].row["fractional"], false)
		}
		if x.price != nil {
			if v, err := baseNotional(x.price, p.Qty, base, rt); err == nil {
				x.pos.Value, x.pos.ValueKnown = v, true
				equity += v
				if sec.Kind == instrument.KindETF {
					// a fund is not an issuer of its provider's other funds: it counts against
					// the single-fund cap and the provider share, and against a company only
					// through holdings that are documented (not available to this review)
					byFund[p.Security] += v
					byProvider[sec.IssuerID] += v
				} else {
					byIssuer[sec.IssuerID] += v
				}
			} else {
				x.why = append(x.why, err.Error())
			}
		} else {
			x.why = append(x.why, "no current price was supplied or stored")
		}
		if !x.pos.ValueKnown {
			unknown++
		}
		hs = append(hs, x)
	}

	var out []holdingReview
	cons := pol.Constraints
	goal := ""
	if v, ok := pol.Goal.TargetDate.Get(); ok {
		goal = string(v)
	}
	for _, x := range hs {
		sec := x.sec
		f := review.Facts{AsOf: day, Equity: equity, PriceFresh: x.pos.ValueKnown, GoalDate: goal}
		f.Missing = append(f.Missing, x.why...)
		f.Caveats = append(f.Caveats, otherNotes...)
		if unknown > 0 {
			f.Caveats = append(f.Caveats, fmt.Sprintf("concentration (rule 3): %d holding(s) have no value, so exposure shares are overstated", unknown))
		}
		// exposures
		if x.pos.ValueKnown && equity > 0 {
			share := func(v int64) money.Decimal {
				d, err := money.FromMinor(v, base)
				if err != nil {
					return money.Decimal{}
				}
				e, _ := money.FromMinor(equity, base)
				q, err := d.Decimal().Div(e.Decimal(), 8, money.HalfEven)
				if err != nil {
					return money.Decimal{}
				}
				return q
			}
			if sec.Kind != instrument.KindETF {
				if s := share(byIssuer[sec.IssuerID]); cons.MaxIssuer.Sign() > 0 && s.Cmp(cons.MaxIssuer) > 0 {
					f.Breaches = append(f.Breaches, review.Breach{Dimension: "issuer", Name: sec.IssuerID, Share: s, Limit: cons.MaxIssuer})
				}
			} else {
				if s := share(byFund[sec.ID]); cons.MaxFund.Sign() > 0 && s.Cmp(cons.MaxFund) > 0 {
					f.Breaches = append(f.Breaches, review.Breach{Dimension: "fund", Name: sec.ID, Share: s, Limit: cons.MaxFund})
				}
				if s := share(byProvider[sec.IssuerID]); cons.MaxProvider.Sign() > 0 && s.Cmp(cons.MaxProvider) > 0 {
					f.Breaches = append(f.Breaches, review.Breach{Dimension: "provider", Name: sec.IssuerID, Share: s, Limit: cons.MaxProvider})
				}
			}
		}
		// recorded decisions: the newest of each that was dated by the as-of day
		var th thesisRec
		if ok, err := latestDated(ctx, rs, recThesis, sec.ID, day, &th, func() string { return th.Day }); err != nil {
			return nil, "", err
		} else if ok {
			f.Thesis = review.Thesis{Status: th.Status, DependsOnDividend: th.DependsOnDividend, Note: th.Note, Day: th.Day}
		}
		var ins instructionRec
		if ok, err := latestDated(ctx, rs, recInstruction, sec.ID, day, &ins, func() string { return ins.Day }); err != nil {
			return nil, "", err
		} else if ok && !ins.Cleared {
			in := &review.Instruction{Action: ins.Action, All: ins.All, Why: ins.Why, By: ins.By, Day: ins.Day}
			if ins.Qty != "" {
				in.Qty = money.MustDecimal(ins.Qty)
			}
			f.Instruction = in
		}
		var lg logRec
		if ok, err := latestDated(ctx, rs, recReviewLog, sec.ID, day, &lg, func() string { return lg.Day }); err != nil {
			return nil, "", err
		} else if ok {
			f.LastReview = lg.Day
		}
		// the strategy's view of a held stock: data state and valuation
		man := plan.Manifest{AsOf: day, PolicyEpoch: rec.Epoch, Policy: rec.Hash, Strategy: review.ID, Config: rcfg.Hash(), Ledger: led.Projection().Fingerprint()}
		if sec.Kind != instrument.KindETF {
			card, err := loadCard(ctx, rs, sec.ID, known, 5)
			if err != nil {
				return nil, "", err
			}
			as, err := loadAssumptions(ctx, rs, sec.ID)
			if err != nil {
				return nil, "", err
			}
			obs, err := db.DistributionObservations(ctx, []string{sec.ID}, known)
			if err != nil {
				return nil, "", err
			}
			payouts := distribution.Resolve(obs, time.Time{})
			prop, err := strategy.Evaluate(strategy.Input{Security: sec.ID, AsOf: day, Card: card, Payouts: payouts, Assumptions: as, Config: scfg, Held: true, Price: x.price})
			if err != nil {
				return nil, "", err
			}
			if prop.DataState != "complete" {
				f.Missing = append(f.Missing, "the strategy's data for this holding is "+prop.DataState)
			}
			if v := prop.Valuation; v != nil && x.price != nil {
				f.Valuation = &review.ValuationFacts{Currency: v.Currency, Bear: v.Bear, Base: v.Base, Bull: v.Bull, Price: x.price.Value, PriceCur: x.price.Currency, PriceDay: x.price.Day}
			}
			q := distribution.Analyse(distribution.Input{Security: sec, Day: day, Payouts: payouts})
			f.DividendCuts, f.DividendCancelled = len(q.Cuts), q.Cancellations
			if len(q.Cuts) > 0 {
				f.Caveats = append(f.Caveats, "no split history was supplied: a dividend cut may be a share split")
			}
			man.Scorecard, man.Assumptions, man.Payouts = card.Hash, prop.Inputs.Assumptions, len(payouts)
		}
		if x.price != nil {
			man.Price = x.price.Day + " " + x.price.Source
		}
		dh, err := decisionsHash(ctx, rs, sec.ID, time.Time{})
		if err != nil {
			return nil, "", err
		}
		man.Decisions = dh
		rv := review.Evaluate(rcfg, x.pos, f)
		out = append(out, holdingReview{Position: x.pos, Name: sec.Name, Review: rv, Manifest: man, Base: base, Held: x.pos.Qty.String(), Free: x.pos.Free.String(), Reviewed: rcfg.Reviewed})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position.Security < out[j].Position.Security })
	return out, led.Book.ID, nil
}

// ledgerAt reads a book now, or as it was recorded by a moment.
func ledgerAt(ctx context.Context, ps store.Portfolios, ref string, at time.Time) (*portfolio.Ledger, error) {
	if at.IsZero() {
		return ps.Ledger(ctx, ref)
	}
	return ps.LedgerAsOf(ctx, ref, at, store.ByRecorded)
}

// openedDay is the day the oldest open lot of a security was acquired.
func openedDay(l *portfolio.Ledger, sec string) string {
	best := ""
	for _, lot := range l.Projection().OpenLots(sec, portfolio.FIFO) {
		if best == "" || lot.Acquired < best {
			best = lot.Acquired
		}
	}
	return best
}

func reviewCard(h holdingReview, bookID, day, expiry string) plan.Card {
	r := h.Review
	c := plan.Card{AsOf: day, Expiry: expiry, Security: h.Position.Security, Name: h.Name, Book: bookID, Sleeve: h.Position.Sleeve, Strategy: review.ID,
		Action: r.Action, DataState: "complete", StrategyState: "reviewed rule set", ExecutionEligibility: "research_only",
		Held: h.Held, Free: h.Free, ReviewBy: r.NextReview,
		Horizon: "a held position: a review date is not a sale date, and a late review is shown as overdue, not as a fresh HOLD",
		Costs:   map[string]string{"proceeds": r.Proceeds, "commission_and_fx": "unavailable: a review does not take a fee schedule", "tax": "unavailable: tax residence and treatment are not stated"},
		ExitRules: []string{
			"1 your instruction (withdrawal or liquidity need)", "2 a thesis you recorded as broken", "3 a concentration breach or the approaching goal",
			"4 valuation above the configured scenario", "5 an event position past its holding limit or invalidated", "6 a scheduled rebalance (not evaluated: no target weights)",
		},
		Invalidation: []string{"a thesis you record as broken proposes a sale of the free shares", "an instruction you record overrides the other rules"},
		WouldChange:  []string{"recording a review (review done) moves the next review date", "recording the thesis or an instruction changes this review", "a new price, a changed assumption or a policy change regenerates it"},
		Alternatives: []string{"keeping the position and reviewing it: nothing forces a sale", "holding cash: no market risk, no income"},
	}
	if !h.Reviewed {
		c.StrategyState = "unreviewed rule set"
	}
	if len(r.Missing) > 0 {
		c.DataState = "insufficient"
	}
	for _, f := range r.Findings {
		c.Reasons = append(c.Reasons, fmt.Sprintf("rule %d (%s): %s", f.Rule, f.Code, f.Detail))
	}
	c.Reasons = append(c.Reasons, r.Conflicts...)
	c.Unknowns = append(append([]string{}, r.Missing...), r.NotEvaluated...)
	if r.Action == "SELL" || r.Action == "REDUCE" {
		c.Sizing = &plan.Sizing{Qty: r.Qty, Base: string(h.Base), Notional: r.ProceedsMinor, Binding: fmt.Sprintf("rule %d", r.Rule)}
	}
	return c
}

func reviewRun(ctx context.Context, a *app, args []string) error {
	fs := newFlags("review run")
	prices := fs.String("prices", "", "CSV with a price for each holding (allocate example shows the columns)")
	asOf := fs.String("as-of", "", "the day, YYYY-MM-DD; default today")
	rates := fs.String("rates", "", "base units per one unit of a foreign currency, e.g. USD=0.92")
	save := fs.Bool("save", false, "store each review as a plan revision (plan list, plan show)")
	valid := fs.Int("valid-days", 7, "days a saved review stays valid")
	asJSON := fs.Bool("json", false, "print as JSON")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: review run BOOK [-prices FILE] [-as-of DAY] [-rates USD=0.92] [-save] [-valid-days N] [-json]")
	}
	if *valid < 1 || *valid > 60 {
		return usageErrorf("-valid-days must be 1-60")
	}
	day, err := today(a, *asOf)
	if err != nil {
		return err
	}
	dayT, _ := time.Parse(time.DateOnly, day)
	var pin time.Time
	if *asOf != "" {
		pin = dayT.Add(24*time.Hour - time.Nanosecond)
	}
	rows, bookID, err := reviewHoldings(ctx, a, book, *prices, *rates, day, pin)
	if err != nil {
		return err
	}
	if *save {
		repo, err := planRepo(a)
		if err != nil {
			return err
		}
		expiry := dayT.AddDate(0, 0, *valid).Format(time.DateOnly)
		for _, h := range rows {
			rev, err := plan.New(reviewCard(h, bookID, day, expiry), h.Manifest, a.now())
			if err != nil {
				return fmt.Errorf("%s: %w", h.Position.Security, err)
			}
			if _, err := repo.Save(ctx, rev, a.now()); err != nil {
				return err
			}
		}
	}
	if *asJSON {
		out := make([]review.Review, 0, len(rows))
		for _, h := range rows {
			out = append(out, h.Review)
		}
		return printJSON(a, out)
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, "nothing is held in this book")
		return nil
	}
	t := cliTable{Header: []string{"security", "state", "rule", "next review", "first reason"}}
	for _, h := range rows {
		r := h.Review
		st := cliGreen
		switch {
		case r.Action == "SELL" || r.Action == "REDUCE":
			st = cliRed
		case r.Status != "ok":
			st = cliYellow
		}
		rule := "-"
		if r.Rule > 0 {
			rule = fmt.Sprint(r.Rule)
		}
		why := "no rule fired"
		if len(r.Findings) > 0 {
			why = r.Findings[0].Detail
		} else if len(r.Missing) > 0 {
			why = "missing: " + r.Missing[0]
		}
		if r.Action == "ABSTAIN" && len(r.Missing) > 0 {
			why = "missing: " + r.Missing[0]
		}
		t.Rows = append(t.Rows, []cliCell{styled(r.Security, cliStrong), styled(r.Label(), st), plain(rule), plain(orDash(r.NextReview)), plain(why)})
	}
	t.write(a.stdout, useColor(a.stdout))
	for _, h := range rows {
		r := h.Review
		fmt.Fprintf(a.stdout, "\n%s %s: %s\n", r.Security, h.Name, r.Label())
		for _, f := range r.Findings {
			fmt.Fprintf(a.stdout, "  rule %d %s: %s\n", f.Rule, f.Code, f.Detail)
		}
		for _, c := range r.Conflicts {
			fmt.Fprintf(a.stdout, "  against: %s\n", c)
		}
		for _, m := range r.Missing {
			fmt.Fprintf(a.stdout, "  missing: %s\n", m)
		}
		for _, n := range r.NotEvaluated {
			fmt.Fprintf(a.stdout, "  not evaluated: %s\n", n)
		}
		if r.Action == "SELL" || r.Action == "REDUCE" {
			fmt.Fprintf(a.stdout, "  proceeds: %s\n", r.Proceeds)
		}
	}
	if *save {
		fmt.Fprintf(a.stdout, "\nsaved as plan revisions (plan list); valid %d days. A saved review is a record, not an order.\n", *valid)
	}
	fmt.Fprintln(a.stdout, "\nthis is a review of what you own: it proposes quantities from free shares only, a review date is not a sale date, and nothing is placed.")
	return nil
}

// latestDated decodes into v the newest record of a key whose own day is not
// after the as-of day. A record dated later is not yet known to a review of that day.
func latestDated(ctx context.Context, rs store.Records, kind, key, day string, v any, dayOf func() string) (bool, error) {
	hist, err := rs.History(ctx, kind, key)
	if err != nil {
		return false, err
	}
	found := false
	for _, r := range hist {
		if err := json.Unmarshal(r.Body, v); err != nil {
			return false, err
		}
		if dayOf() <= day {
			found = true
			continue
		}
	}
	if !found {
		return false, nil
	}
	// decode again the last one that qualified
	for i := len(hist) - 1; i >= 0; i-- {
		if err := json.Unmarshal(hist[i].Body, v); err != nil {
			return false, err
		}
		if dayOf() <= day {
			return true, nil
		}
	}
	return false, nil
}
