package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/b070nd/stoxicity/app/src/csvtable"
	"github.com/b070nd/stoxicity/app/src/eventfact"
)

// analystImportUsage is the description of the file `eventdb import-analyst` reads.
const analystImportUsage = `usage: eventdb import-analyst -file F [-dry-run]

F is a CSV file with a header row, one analyst revision per row.
Required columns: firm, symbol, kind, published_at, source_ref
Optional columns: cik, available_at, held_at, previous_rating, new_rating,
  previous_target, new_target, previous_estimate, new_estimate, metric,
  period, currency

  kind          upgrade, downgrade, target_raised, target_cut, estimate_raised
                or estimate_cut
  symbol        a ticker the reference data knows (cik, when given, must match)
  published_at  RFC 3339 with an offset (2026-11-03T14:30:00-05:00), or a day
                (2026-11-03), which is read as that whole day: precision "day"
  available_at  RFC 3339 with an offset, when a reader could first act on it;
                when absent it is published_at
  held_at       RFC 3339 with an offset, when your own system held the
                revision; within 72 hours of published_at the event counts as
                known, otherwise (or without held_at) as reconstructed
  ratings       a rating revision needs new_rating
  targets       a target revision needs new_target and currency; the price is
                above zero
  estimates     an estimate revision needs new_estimate, currency, period and
                metric (eps or revenue)
  source_ref    a URL or the vendor's identifier of the record

One revision is one event: firm, symbol, kind, published day (as the file
writes it), period and, for an estimate, metric make its identity, so a
corrected row imported again replaces the earlier figures and a repeated row
in one file keeps the later one. Figures are stored as stated, nothing is
computed, and no comparison with a consensus is made. A row that cannot be
stored is left out and counted by reason. -dry-run reads and checks the file
without writing.`

// labEventdbImportAnalyst stores a file of analyst revisions as economic events.
func labEventdbImportAnalyst(ctx context.Context, a *app, args []string) error {
	fs := newFlags("eventdb import-analyst")
	file := fs.String("file", "", "CSV file of analyst revisions")
	dry := fs.Bool("dry-run", false, "read and check the file, write nothing")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("eventdb import-analyst takes no arguments, got %q\n%s", rest[0], analystImportUsage)
	}
	if *file == "" {
		return usageErrorf("eventdb import-analyst needs -file\n%s", analystImportUsage)
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	required, optional := eventfact.AnalystColumns()
	rows, err := csvtable.Read(f, required, optional)
	f.Close()
	if err != nil {
		return err
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	refs, err := db.RefTickers(ctx)
	if err != nil {
		return err
	}
	ciks := map[string]string{}
	for _, r := range refs {
		if cur, ok := ciks[r.Ticker]; !ok || (cur == "" && r.CIK != "") {
			ciks[r.Ticker] = r.CIK
		}
	}
	imp := eventfact.FromAnalystRows(rows, func(symbol string) (string, bool) {
		cik, ok := ciks[symbol]
		return cik, ok
	})

	var fresh, changed, same int
	for _, r := range imp.Records {
		hist, err := db.EventFactHistory(ctx, r.ID)
		if err != nil {
			return err
		}
		switch {
		case len(hist) == 0:
			fresh++
		case eventfact.Hash(hist[len(hist)-1].Record) == eventfact.Hash(r):
			same++
		default:
			changed++
		}
	}
	if !*dry {
		if _, err := db.PutEventFacts(ctx, imp.Records, a.now()); err != nil {
			return err
		}
	}

	rejected := 0
	for _, n := range imp.Rejected {
		rejected += n
	}
	verb := "stored"
	if *dry {
		verb = "would be stored"
	}
	fmt.Fprintf(a.stdout, "rows read: %d\n", len(rows))
	fmt.Fprintf(a.stdout, "revisions %s: %d new, %d corrected, %d unchanged\n", verb, fresh, changed, same)
	fmt.Fprintf(a.stdout, "rows rejected: %d\n", rejected)
	if rejected > 0 {
		t := cliTable{Header: []string{"reason", "rows"}, Right: []bool{false, true}}
		for _, reason := range labKeys(imp.Rejected) {
			t.Rows = append(t.Rows, []cliCell{plain(reason), plain(strconv.Itoa(imp.Rejected[reason]))})
		}
		t.write(a.stdout, useColor(a.stdout))
	}
	return nil
}
