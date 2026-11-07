package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/b070nd/stoxicity/app/src/valuation"
)

// fundamentalsCoverage measures how far the capitalisations are done: of the
// issuers with common stock or receipts in the list, how many have been looked
// at, are valued, are mapped, are stale, ambiguous, failed or unsupported, for
// all of them and for the ones followed, and why each one that has no value has
// none. It reads what is stored and makes no request.
func (a *app) fundamentalsCoverage(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals coverage")
	list := fs.String("list", "", "list the issuers in these states (comma separated: "+statesText()+"), or all")
	onlyFollowed := fs.Bool("followed", false, "list only the issuers you follow")
	if _, err := parseMixed(fs, args); err != nil {
		return err
	}
	want := map[valuation.State]bool{}
	if *list != "" {
		for _, w := range strings.Split(*list, ",") {
			w = strings.TrimSpace(strings.ToLower(w))
			if w == "all" {
				for _, s := range valuation.States {
					want[s] = true
				}
				continue
			}
			ok := false
			for _, s := range valuation.States {
				if string(s) == w {
					want[s], ok = true, true
				}
			}
			if !ok {
				return usageErrorf("-list: %q is not a state (%s, or all)", w, statesText())
			}
		}
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	profiles := a.universe().All()
	now := a.now()
	set, err := valuation.Build(ctx, db, profiles, valuation.View{}, now)
	if err != nil {
		return err
	}
	raw, err := db.CoverChecks(ctx)
	if err != nil {
		return err
	}
	checks := make(map[string]valuation.Check, len(raw))
	for cik, c := range raw {
		checks[cik] = valuation.Check{At: c.At, Problem: c.Problem, Kind: c.Kind, Failures: c.Failures, Next: c.Next}
	}
	followed := map[string]bool{}
	for _, t := range interestingTickers(ctx, db, a.knownTickers(), a.layerPath(), a.cfg.Lens.Threshold, now) {
		if p, ok := a.universe().Get(t); ok && p.CIK != "" {
			followed[p.CIK] = true
		}
	}
	cov := valuation.Measure(profiles, set, checks, followed, now)
	if cov.All.Issuers == 0 {
		fmt.Fprintln(a.stdout, "no issuer with a SEC number and common stock is in the company list: run stoxicity universe update")
		return nil
	}
	color := useColor(a.stdout)
	t := cliTable{Header: []string{"", "ISSUERS", "CHECKED", "MAPPED", "VALUED", "STALE", "INCOMPLETE", "UNRESOLVED", "UNSUPPORTED", "FAILED", "NOT READ YET"},
		Right: []bool{false, true, true, true, true, true, true, true, true, true, true}}
	row := func(name string, tl valuation.Tally) []cliCell {
		n := func(s valuation.State, st cliStyle) cliCell {
			if tl.ByState[s] == 0 {
				return styled("0", cliDim)
			}
			return styled(fmt.Sprint(tl.ByState[s]), st)
		}
		return []cliCell{styled(name, cliStrong), plain(fmt.Sprint(tl.Issuers)), plain(fmt.Sprint(tl.Checked)), plain(fmt.Sprint(tl.Mapped)),
			n(valuation.StateValued, cliGreen), n(valuation.StateStale, cliYellow), n(valuation.StateIncomplete, cliYellow), n(valuation.StateUnresolved, cliYellow),
			n(valuation.StateUnsupported, cliDim), n(valuation.StateFailed, cliRed), n(valuation.StatePending, cliDim)}
	}
	t.Rows = [][]cliCell{row("all issuers", cov.All), row("followed", cov.Followed)}
	t.write(a.stdout, color)
	fmt.Fprintf(a.stdout, "mapped: every class of the issuer is tied to a security (valued issuers are mapped; so are some that wait for a price). %d issuer(s) wait for a retry after a failed look.\n", cov.All.Retrying)
	fmt.Fprintln(a.stdout, "stale: a count or a price is too old · incomplete: something is missing that more data may supply · unresolved: the evidence does not decide · unsupported: this method cannot value it · failed: the last look failed · not read yet: the cover page has not been read")
	if len(want) == 0 {
		fmt.Fprintln(a.stdout, "list the issuers behind a number with -list stale,failed,... (or all), -followed for the ones you follow")
		return nil
	}
	lt := cliTable{Header: []string{"TICKER", "FOLLOWED", "STATE", "CLASSES", "COUNT DATE", "PRICE DATE", "FAILURES", "NEXT TRY", "WHY"}, Right: []bool{false, false, false, true, false, false, true, false, false}}
	shown := 0
	for _, s := range cov.Issuers {
		if !want[s.State] || (*onlyFollowed && !s.Followed) {
			continue
		}
		shown++
		fol := ""
		if s.Followed {
			fol = "yes"
		}
		next := ""
		if !s.Next.IsZero() {
			next = s.Next.UTC().Format("2006-01-02 15:04Z")
		}
		lt.Rows = append(lt.Rows, []cliCell{styled(s.Ticker, cliStrong), plain(fol), styled(string(s.State), stateStyle(s.State)),
			plain(fmt.Sprintf("%d of %d valued", s.Valued, s.Classes)), plain(orDash(s.CountDay)), plain(orDash(s.PriceDay)), plain(fmt.Sprint(s.Failures)), plain(next), plain(s.Reason)})
	}
	if shown == 0 {
		fmt.Fprintln(a.stdout, "no issuer in those states")
		return nil
	}
	fmt.Fprintln(a.stdout)
	lt.write(a.stdout, color)
	return nil
}

func statesText() string {
	parts := make([]string, len(valuation.States))
	for i, s := range valuation.States {
		parts[i] = string(s)
	}
	return strings.Join(parts, ", ")
}

func stateStyle(s valuation.State) cliStyle {
	switch s {
	case valuation.StateValued:
		return cliGreen
	case valuation.StateFailed:
		return cliRed
	case valuation.StateUnsupported, valuation.StatePending:
		return cliDim
	}
	return cliYellow
}
