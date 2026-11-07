package main

import (
	"context"
	"fmt"
	"time"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
)

// universeFundamentals reads the SEC's XBRL frames: revenue, net income,
// assets, equity and shares of every filer, in a few dozen small requests. It
// does nothing when the last reading is younger than a week, unless -force.
func (a *app) universeFundamentals(ctx context.Context, args []string) error {
	fs := newFlags("universe fundamentals")
	force := fs.Bool("force", false, "read the market even if the last reading is young")
	if _, err := parseMixed(fs, args); err != nil {
		return err
	}
	if a.cfg.Company.Fundamentals == "off" {
		return fmt.Errorf("fundamentals are off: set company.fundamentals to sec")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	client, err := a.httpClient()
	if err != nil {
		return err
	}
	k := a.fundamentalsKeeper(db, client)
	rep, ran, err := k.Refresh(ctx, *force)
	if err != nil {
		return err
	}
	if !ran {
		at, _ := db.FundamentalsRead(ctx)
		fmt.Fprintf(a.stdout, "fundamentals: read %s ago; -force reads again\n", a.now().Sub(at).Round(time.Minute))
		return nil
	}
	fmt.Fprintf(a.stdout, "fundamentals: %d facts of %d companies (%d requests, %d frames not published yet)\n",
		rep.Facts, rep.Companies, rep.Requests, rep.Missing)
	facts, err := db.Fundamentals(ctx)
	if err != nil {
		return err
	}
	sums := fundamentals.Summarize(facts)
	have := map[string]int{}
	for _, s := range sums {
		for k := range s.Num {
			have[k]++
		}
	}
	fmt.Fprintf(a.stdout, "companies with: revenue %d, income %d, growth %d, margin %d, equity %d, roe %d, shares %d\n",
		have[fundamentals.Revenue], have[fundamentals.NetIncome], have["growth"], have["margin"], have[fundamentals.Equity], have["roe"], have[fundamentals.Shares])
	return nil
}
