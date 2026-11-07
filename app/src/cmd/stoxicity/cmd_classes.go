package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/valuation"
)

// fundamentalsClasses shows the share count of each class of a company's stock
// as its newest report's cover page states it, how each class is mapped to a
// security, and the value of each class and of the issuer. The cover page is
// read first unless -stored; with -at only the counts and mappings known by
// then are shown (prices are kept for the newest session only).
func (a *app) fundamentalsClasses(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals classes")
	offline := fs.Bool("stored", false, "do not ask the SEC, read what is stored")
	at := fs.String("at", "", "the counts and mappings known at this time (RFC 3339 or YYYY-MM-DD), no values")
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
	var known time.Time
	if *at != "" {
		if known, err = parseWhen(*at); err != nil {
			return err
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
	pick, ok := valuation.Select(mine, known)
	if !ok {
		return fmt.Errorf("no cover page share count is known for %s%s", p.Ticker, atText(known))
	}
	maps, err := db.ClassMaps(ctx, known)
	if err != nil {
		return err
	}
	mp := map[string]valuation.Mapping{}
	for d, m := range maps[p.CIK] {
		mp[d] = m
	}
	fmt.Fprintf(a.stdout, "%s: cover page of %s %s, filed %s%s\n", p.Ticker, pick.Form, pick.Accession, pick.Filed, atText(known))

	var val valuation.Value
	if known.IsZero() {
		m, err := valuation.LoadMarket(ctx, db)
		if err != nil {
			return err
		}
		types := map[string]string{}
		for _, q := range a.universe().All() {
			types[strings.ToUpper(q.Ticker)] = q.Type()
		}
		for d, m := range mp {
			m.Type = types[strings.ToUpper(m.Ticker)]
			mp[d] = m
		}
		val = valuation.Compute(pick, mp, m, a.now())
	}
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
		row := []cliCell{plain(c.Name()), styled(m.Ticker, cliStrong), styled(status, sstyle), plain(c.AsOf), plain(countText(c.Shares) + note), plain(""), plain(""), plain(""), styled("no value without today's price", cliDim)}
		if known.IsZero() {
			cv := val.Classes[i]
			row[5], row[6], row[7] = plain(fmt.Sprintf("%g", cv.SplitFactor)), plain(fmt.Sprintf("%.2f", cv.Price.Close)), plain(cv.Price.Day)
			if cv.Price.Close == 0 {
				row[5], row[6], row[7] = plain(""), plain(""), plain("")
			}
			if cv.OK {
				row[8] = styled(moneyText(cv.Cap), cliGreen)
			} else {
				row[8] = styled("unavailable", cliRed)
			}
		}
		t.Rows = append(t.Rows, row)
	}
	t.write(a.stdout, color)
	if known.IsZero() {
		for _, cv := range val.Classes {
			if !cv.OK {
				fmt.Fprintf(a.stdout, "  %s: %s\n", cv.Name(), cv.Reason)
			}
		}
		if val.IssuerOK {
			fmt.Fprintf(a.stdout, "issuer capitalisation (every class): %s\n", moneyText(val.Issuer))
		} else {
			fmt.Fprintf(a.stdout, "issuer capitalisation: unavailable (%s)\n", val.IssuerReason)
		}
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
	for _, c := range pick.Classes {
		if m := mp[c.Dimensions]; m.Status != "" && m.Basis != "" {
			fmt.Fprintf(a.stdout, "  mapping of %s: %s\n", c.Name(), m.Basis)
		}
	}
	return nil
}

func atText(known time.Time) string {
	if known.IsZero() {
		return ""
	}
	return " as known at " + known.UTC().Format(time.RFC3339)
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
