package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"text/tabwriter"

	"github.com/b070nd/stoxicity/app/src/scheduler"
)

const lastErrorWidth = 60

// cmdTenant lists the tenants.
func cmdTenant(_ context.Context, a *app, args []string) error {
	if len(args) != 1 || args[0] != "list" {
		return usageErrorf("expected: list")
	}
	list, err := a.tenants().List()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tDEFAULT WATCH\tWATCHES")
	for _, t := range list {
		count := "?"
		if ws, err := a.watches(t.ID).List(); err == nil {
			count = fmt.Sprint(len(ws))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Name, t.DefaultWatch, count)
	}
	return tw.Flush()
}

// cmdSources lists the sources with the health the store last saw.
func cmdSources(ctx context.Context, a *app, args []string) error {
	if len(args) != 1 || args[0] != "list" {
		return usageErrorf("expected: list")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	loc := a.location()
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tENABLED\tLICENCE\tHEALTH\tLAST OK\tLAST ERROR")
	for _, id := range slices.Sorted(maps.Keys(a.reg)) {
		src := a.reg[id]
		_, h, err := db.LoadSource(ctx, id)
		if err != nil {
			return err
		}
		health, lastOK := "-", "-"
		if !h.LastOK.IsZero() || h.Failures > 0 {
			health = h.Level
			if health == "" {
				health = scheduler.LevelYellow
			}
		}
		if !h.LastOK.IsZero() {
			lastOK = h.LastOK.In(loc).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%s\t%s\n", id, src.Kind, src.Enabled, src.Licence,
			health, lastOK, truncate(oneLine(h.LastError), lastErrorWidth))
	}
	return tw.Flush()
}

// truncate shortens s to at most n runes, ending in "..." if it was cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}
