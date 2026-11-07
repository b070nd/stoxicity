package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/valuation"
)

// fundamentalsClasses shows the share count of each class of a company's stock
// as its newest report's cover page states it, how each class is mapped to a
// security, and the value of each class and of the issuer, with the inputs of
// each value. The cover page is read first unless -stored. With -at the
// valuation is of that time: by default as this installation had recorded it
// ("known"), or with -basis reconstructed as the record supports it today; the
// two are labelled and never mixed.
func (a *app) fundamentalsClasses(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals classes")
	offline := fs.Bool("stored", false, "do not ask the SEC, read what is stored")
	at := fs.String("at", "", "value as of this time (RFC 3339 or YYYY-MM-DD)")
	basis := fs.String("basis", "known", "with -at: known (what this installation had recorded then) or reconstructed (what the record supports now)")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("classes takes one ticker")
	}
	p, ok := a.universe().Get(rest[0])
	if !ok || p.CIK == "" {
		return fmt.Errorf("%s has no SEC number in the company list", strings.ToUpper(rest[0]))
	}
	var view valuation.View
	if *at != "" {
		known, err := parseWhen(*at)
		if err != nil {
			return err
		}
		switch *basis {
		case "known":
			view = valuation.View{Mode: valuation.ModeKnown, At: known}
		case "reconstructed":
			view = valuation.View{Mode: valuation.ModeReconstructed, At: known}
		default:
			return usageErrorf("-basis must be known or reconstructed")
		}
		*offline = true
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	if !*offline {
		client, err := a.httpClient()
		if err != nil {
			return err
		}
		k := a.coverKeeper(db, client)
		if k == nil {
			return fmt.Errorf("fundamentals are off: set company.fundamentals to sec")
		}
		rep, stored, err := k.Read(ctx, p.CIK)
		if err != nil {
			return err
		}
		if stored {
			fmt.Fprintf(a.stdout, "read the cover page of %s (%d requests)\n", p.Ticker, rep.Requests)
		} else {
			fmt.Fprintln(a.stdout, "the newest report's cover page was already stored")
		}
	}
	all, err := db.ClassObs(ctx)
	if err != nil {
		return err
	}
	var mine []valuation.Obs
	for _, o := range all {
		if o.CIK == p.CIK {
			mine = append(mine, o)
		}
	}
	pick, ok := valuation.SelectView(mine, view)
	if !ok {
		return fmt.Errorf("no cover page share count is known for %s%s", p.Ticker, atText(view))
	}
	var mapAt time.Time
	if view.Mode == valuation.ModeKnown {
		mapAt = view.At
	}
	maps, err := db.ClassMaps(ctx, mapAt)
	if err != nil {
		return err
	}
	mp := valuation.Annotate(maps[p.CIK], a.universe().All())
	fmt.Fprintf(a.stdout, "%s: cover page of %s %s, filed %s%s\n", p.Ticker, pick.Form, pick.Accession, pick.Filed, atText(view))
	if c := view.Caveat(); c != "" {
		fmt.Fprintf(a.stdout, "  %s\n", c)
	}

	m, err := valuation.LoadMarketView(ctx, db, view)
	if err != nil {
		return err
	}
	val := valuation.Compute(pick, mp, m, view.Ref(a.now()))
	color := useColor(a.stdout)
	t := cliTable{
		Header: []string{"CLASS", "SYMBOL", "MAPPING", "COUNT DATE", "SHARES", "SPLIT", "PRICE", "SESSION", "CLASS VALUE"},
		Right:  []bool{false, false, false, false, true, true, true, false, true},
	}
	for i, c := range pick.Classes {
		m := mp[c.Dimensions]
		status, sstyle := "not mapped yet", cliYellow
		switch m.Status {
		case valuation.Mapped:
			status, sstyle = fmt.Sprintf("mapped (%s %.2f)", m.Method, m.Confidence), cliGreen
		case valuation.Ambiguous:
			status, sstyle = "ambiguous", cliRed
		case valuation.Unmapped:
			status, sstyle = "not listed", cliYellow
		}
		note := ""
		if c.Revised {
			note = " (revised)"
		}
		cv := val.Classes[i]
		row := []cliCell{plain(c.Name()), styled(m.Ticker, cliStrong), styled(status, sstyle), plain(c.AsOf), plain(countText(c.Shares) + note), plain(""), plain(""), plain(""), styled("unavailable", cliRed)}
		if cv.Price.Close != 0 {
			row[5], row[6], row[7] = plain(fmt.Sprintf("%g", cv.SplitFactor)), plain(priceText(cv)), plain(cv.Price.Day)
		}
		if cv.OK {
			row[8] = styled(moneyText(cv.Cap)+" "+cv.Currency, cliGreen)
		}
		t.Rows = append(t.Rows, row)
	}
	t.write(a.stdout, color)
	valued := 0
	for _, cv := range val.Classes {
		if cv.OK {
			valued++
		} else {
			fmt.Fprintf(a.stdout, "  %s: %s\n", cv.Name(), cv.Reason)
		}
	}
	if val.IssuerOK {
		fmt.Fprintf(a.stdout, "issuer capitalisation (every class): %s %s\n", moneyText(val.Issuer), val.Currency)
	} else {
		fmt.Fprintf(a.stdout, "issuer capitalisation: unavailable (%s)\n", val.IssuerReason)
	}
	fmt.Fprintf(a.stdout, "completeness: %d of %d classes valued", valued, len(val.Classes))
	if len(pick.Dropped) > 0 {
		fmt.Fprintf(a.stdout, ", %d class(es) the report before stated are missing", len(pick.Dropped))
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "inputs:")
	for _, cv := range val.Classes {
		a.classInputs(cv, val.Coverage)
	}
	for _, c := range pick.Classes {
		if c.Revised {
			fmt.Fprintf(a.stdout, "  %s: %s stated %s on %s; this filing states %s\n", c.Name(), c.WasAccession, countText(c.Was), c.AsOf, countText(c.Shares))
		}
		if iss := c.Issue(); iss != "" {
			fmt.Fprintf(a.stdout, "  %s\n", iss)
		}
	}
	for _, d := range pick.Dropped {
		fmt.Fprintf(a.stdout, "  %s was stated by the report before and is not stated by this one\n", valuation.ClassName(d))
	}
	return nil
}

// priceText writes a class's price with the currency it is quoted in.
func priceText(cv valuation.ClassValue) string {
	if cv.QuoteCurrency == "" {
		return fmt.Sprintf("%.2f (currency unknown)", cv.Price.Close)
	}
	return fmt.Sprintf("%.2f %s", cv.Price.Close, cv.QuoteCurrency)
}

// classInputs lists what one class's value rests on: the count and its filing,
// the mapping, the price session with its currency and when it was stored, the
// splits, and the rate if one converted the price.
func (a *app) classInputs(cv valuation.ClassValue, cov marketdata.SplitCoverage) {
	w := a.stdout
	fmt.Fprintf(w, "  %s\n", cv.Name())
	fmt.Fprintf(w, "    count: %s shares on %s, %s %s filed %s, stored %s\n", countText(cv.Shares), cv.AsOf, cv.Form, cv.Accession, cv.Filed, whenText(cv.Received))
	if cv.Mapping.Status == "" {
		fmt.Fprintln(w, "    mapping: none")
	} else {
		fmt.Fprintf(w, "    mapping: %s %s by %s on %s: %s\n", cv.Mapping.Status, cv.Mapping.Ticker, cv.Mapping.Method, whenText(cv.Mapping.MappedAt), cv.Mapping.Basis)
	}
	if cv.Price.Close != 0 {
		fmt.Fprintf(w, "    price: %.2f on %s, raw %t, stored %s\n", cv.Price.Close, cv.Price.Day, cv.Price.Raw, whenText(cv.Price.Fetched))
		if cv.QuoteCurrency != "" {
			fmt.Fprintf(w, "    currency: %s (%s)\n", cv.QuoteCurrency, cv.CurrencyBasis)
		} else {
			fmt.Fprintln(w, "    currency: unknown")
		}
		if cov.Since != "" {
			fmt.Fprintf(w, "    splits: %d between the count and the price (factor %g); the split list covers %s on, read %s\n", len(cv.Splits), cv.SplitFactor, cov.Since, whenText(cov.Read))
		} else {
			fmt.Fprintln(w, "    splits: the split list has not been read")
		}
		for _, s := range cv.Splits {
			fmt.Fprintf(w, "      %s %g for %g\n", s.Day, s.To, s.From)
		}
		if cv.FX != nil {
			fmt.Fprintf(w, "    rate: %s to %s %g on %s from %s\n", cv.FX.From, cv.FX.To, cv.FX.Rate, cv.FX.Day, cv.FX.Source)
		}
	} else {
		fmt.Fprintln(w, "    price: none stored for the view")
	}
}

func whenText(t time.Time) string {
	if t.IsZero() {
		return "(time not recorded)"
	}
	return t.UTC().Format("2006-01-02 15:04Z")
}

func atText(v valuation.View) string {
	if !v.Historical() {
		return ""
	}
	if v.Mode == valuation.ModeKnown {
		return " as known at " + v.At.UTC().Format(time.RFC3339)
	}
	return " reconstructed for " + v.At.UTC().Format(time.RFC3339)
}

// moneyText writes an amount of dollars with thousands separators.
func moneyText(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// fundamentalsCovers reads the cover pages that are due, as the background cycle
// does, a budget of them.
func (a *app) fundamentalsCovers(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals covers")
	limit := fs.Int("max", a.cfg.Company.CoversPerCycle, "read at most N companies' cover pages")
	if _, err := parseMixed(fs, args); err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	k := a.coverKeeper(db, client)
	if k == nil {
		return fmt.Errorf("fundamentals are off: set company.fundamentals to sec")
	}
	rep, err := k.Refresh(ctx, *limit)
	fmt.Fprintf(a.stdout, "covers: %d companies looked at, %d reports stored, %d unchanged, %d without a machine-readable cover, %d failed, %d still due (%d requests, %d documents read whole)\n",
		rep.Asked, rep.Stored, rep.Unchanged, rep.Empty, rep.Bad, rep.Left, rep.Requests, rep.Full)
	return err
}
