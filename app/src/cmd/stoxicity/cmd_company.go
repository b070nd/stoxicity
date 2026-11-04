package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"
)

// cmdCompany manages and searches the company master.
func cmdCompany(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: status | update [-force] | find TEXT...")
	}
	switch args[0] {
	case "status":
		if len(args) > 1 {
			return usageErrorf("status takes no arguments")
		}
		return a.companyStatus()
	case "update":
		fs := newFlags("company update")
		force := fs.Bool("force", false, "ask the SEC even if the cached list is young")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) > 0 {
			return usageErrorf("unexpected argument %q", rest[0])
		}
		res, err := a.refreshCompanies(ctx, *force)
		if err != nil {
			return err
		}
		switch {
		case res.Skipped:
			fmt.Fprintf(a.stdout, "the cached list is younger than company.max_age (%d tickers); use -force to ask anyway\n", res.Records)
		case res.Updated:
			fmt.Fprintf(a.stdout, "stored a new list: %d tickers\n", res.Records)
		default:
			fmt.Fprintf(a.stdout, "the SEC says the list is unchanged: %d tickers\n", res.Records)
		}
		return nil
	case "find":
		query := strings.Join(args[1:], " ")
		if strings.TrimSpace(query) == "" {
			return usageErrorf("find needs text to look for")
		}
		m := a.companies()
		if m.Empty() {
			return fmt.Errorf("no company list yet: run \"stoxicity company update\"")
		}
		found := m.Find(query, 20)
		if len(found) == 0 {
			fmt.Fprintln(a.stdout, "no company matches")
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "TICKER\tCIK\tNAME")
		for _, e := range found {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Ticker, e.CIK, e.Name)
		}
		return tw.Flush()
	}
	return usageErrorf("unknown subcommand %q (expected status, update or find)", args[0])
}

func (a *app) companyStatus() error {
	m := a.companies()
	fmt.Fprintf(a.stdout, "folder    %s\n", a.companyDir())
	if m.Empty() {
		fmt.Fprintln(a.stdout, "list      none yet: run \"stoxicity company update\"")
		return nil
	}
	fmt.Fprintf(a.stdout, "tickers   %d\n", len(m.Entities))
	if m.FetchedAt.IsZero() {
		fmt.Fprintln(a.stdout, "checked   unknown")
	} else {
		age := m.Age(a.now()).Round(time.Hour)
		fmt.Fprintf(a.stdout, "checked   %s (%s ago)\n", m.FetchedAt.In(a.location()).Format("2006-01-02 15:04 MST"), age)
	}
	fmt.Fprintf(a.stdout, "refresh   %v (company.enabled), every %s at most\n", a.cfg.Company.Enabled, a.cfg.Company.MaxAge)
	return nil
}
