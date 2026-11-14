package measure

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

func pct(n, of int) string {
	if of == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(n)/float64(of))
}

func mb(b int64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }

func dur(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

// Markdown writes the report for people.
func (r Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# Historical data feasibility measurement (PRD 7.1)\n\n")
	w("- **Window:** %s to %s (%d days)\n", r.From, r.To, r.Days)
	w("- **Code revision:** `%s`\n", r.Revision)
	w("- **Generated:** %s\n", r.Generated.UTC().Format(time.RFC3339))
	w("- **Run:** read-only against the configured data: everything was written to a scratch data folder; no paid service, no trade, no document text stored.\n")
	if len(r.Settings) > 0 {
		w("- **Settings:** %s\n", strings.Join(r.Settings, "; "))
	}
	w("\n## Recommendation: %s\n\n", r.Recommendation)
	for _, c := range r.Conditions {
		w("- %s\n", c)
	}
	if len(r.Conditions) == 0 {
		w("No conditions.\n")
	}

	w("\n## 1. What the free sources hold\n\n")
	w("EDGAR daily indexes: %d days, %d complete, %d without filings, %d unresolved.\n\n", r.IndexDays.Days, r.IndexDays.Complete, r.IndexDays.NonFiling, r.IndexDays.Unresolved)
	w("| Form | Filings | Companies | Acceptance time known | Day only | Not in the SEC record | Record not read (budget) | With a ticker |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, f := range append(slices.Clone(r.Forms), r.Totals) {
		w("| %s | %d | %d | %s | %s | %s | %s | %s |\n", f.Form, f.Filings, f.Companies, pct(f.TimeKnown, f.Filings), pct(f.DayOnly, f.Filings), pct(f.NotInRecord, f.Filings), pct(f.NotAsked, f.Filings), pct(f.WithTicker, f.Filings))
	}
	w("\n\"With a ticker\" is the identity check: a filing without one belongs to a company the SEC ticker list does not name (a fund, a private filer, a person filing a Form 4 for themself). Its events cannot be tied to a price series.\n")
	if len(r.ItemCounts) > 0 {
		type kv struct {
			k string
			v int
		}
		var items []kv
		for k, v := range r.ItemCounts {
			items = append(items, kv{k, v})
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].v > items[j].v || items[i].v == items[j].v && items[i].k < items[j].k
		})
		var parts []string
		for i, it := range items {
			if i == 12 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s: %d", it.k, it.v))
		}
		w("\n8-K items (most frequent): %s.\n", strings.Join(parts, ", "))
	}

	w("\n## 2. Missing data against unrecognized data\n\n")
	w("| Category | Named by form or item (filings / companies) | Only the text tells (filings) | Records made | Securities | With figures | Verdict |\n|---|---:|---:|---:|---:|---:|---|\n")
	for _, c := range r.Categories {
		w("| %s | %d / %d | %d | %d | %d | %d | **%s** |\n", c.Name, c.Structured, c.Companies, c.TextOnly, c.Records, c.Symbols, c.WithFigures, c.Verdict)
	}
	w("\n")
	for _, c := range r.Categories {
		if c.Reason != "" {
			w("- **%s:** %s.\n", c.Name, c.Reason)
		}
	}
	w("\nVerdicts: *not recognized* means the free data names the category and the pipeline made nothing of it (extraction work); *not fetched* means only a document's text or XML tells, which this run did not read (free sourcing work); *no free source* means a purchase decision.\n")
	if r.KindByItems > 0 {
		w("\nAt the event level, %d 8-K filings have items that name an event kind; the pipeline gave %d of them that kind (%s).\n", r.KindByItems, r.KindAssigned, pct(r.KindAssigned, r.KindByItems))
	}
	if r.Insiders.Asked > 0 {
		w("\nForm 4 sample: %d filings read, %d trades in ordinary shares, %d without one, %d unreadable, %d left unread.\n", r.Insiders.Asked, r.Insiders.Trades, r.Insiders.Empty, r.Insiders.Unreadable, r.Insiders.Left)
	}
	w("\nEconomic events in the window: %d (%d held at the time, %d rebuilt later).", r.Records, r.Known, r.Rebuilt)
	for _, c := range r.Categories {
		if c.Name == "earnings" && c.Records > 0 {
			w(" Deduplication: %d earnings filings (8-K 2.02, 10-Q, 10-K) became %d earnings records, %s filings per record.", c.Structured, c.Records, ratio(c.Structured, c.Records))
		}
	}
	w("\n")

	w("\n## 3. Prices and benchmark\n\n")
	if !r.Prices.Checked {
		w("Not checked: %s.\n", r.Prices.Note)
	} else {
		w("| | Events with a symbol | Entry session bar | Bar 20 sessions later |\n|---|---:|---:|---:|\n")
		w("| Events | %d | %s | %s |\n", r.Prices.Events, pct(r.Prices.Entry, r.Prices.Events), pct(r.Prices.Horizon20, r.Prices.Events))
		w("| Benchmark %s on those days | | %s | %s |\n", r.Prices.Benchmark, pct(r.Prices.BenchEntry, r.Prices.Events), pct(r.Prices.BenchHorizon20, r.Prices.Events))
		if r.Prices.Note != "" {
			w("\n%s\n", r.Prices.Note)
		}
	}

	w("\n## 4. What the run cost\n\n")
	w("%d requests, %s downloaded, %d failed; %s in all.\n\n", r.Requests, mb(r.Bytes), r.Failures, dur(r.Elapsed))
	w("| Host | Requests | Downloaded | Failed |\n|---|---:|---:|---:|\n")
	var hosts []string
	for h := range r.Hosts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		x := r.Hosts[h]
		w("| %s | %d | %s | %d |\n", h, x.Requests, mb(x.Bytes), x.Failures)
	}
	w("\n| Step | Time | Note |\n|---|---:|---|\n")
	for _, s := range r.Stages {
		w("| %s | %s | %s |\n", s.Name, dur(s.Duration), s.Note)
	}
	w("\nRetries happen inside the HTTP client and count once here. Scratch database after the run: %s.\n", mb(r.StoreSize))
	if r.Stopped != "" {
		w("\n**Stopped early:** %s.\n", r.Stopped)
	}

	p := r.Projection
	w("\n## 5. Projected six-month backfill\n\n")
	w("| | Estimate |\n|---|---:|\n")
	w("| Filings | %d |\n| SEC requests | %d |\n| Downloaded | %s |\n| Database growth | %s |\n| Time | %s |\n| Price requests | %d |\n| External cost | %s |\n",
		p.Filings, p.Requests, mb(p.Bytes), mb(p.Storage), dur(p.Elapsed), p.PriceRequests, p.Cost)
	w("\n")
	for _, n := range p.Notes {
		w("- %s\n", n)
	}
	w("\nThis report measures data and the pipeline. It does not say that any strategy is profitable.\n")
	return b.String()
}

func ratio(a, b int) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", float64(a)/float64(b))
}
