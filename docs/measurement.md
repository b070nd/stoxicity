# Historical data feasibility measurement

`stoxicity measure` answers one question before any large backfill: what do the free sources hold for a
window of history, what does the current pipeline make of it, and what would six months cost? It is
step 7.1 of the SCY-1 PRD and phase 3 of its milestones. It decides nothing by itself: its report ends in
a GO, CONDITIONAL GO or NO-GO recommendation for a person to accept.

```text
stoxicity measure -from DAY -to DAY -scratch DIR [-out FILE.md] [-max-requests 40000] [-max-mb 2048]
                  [-insiders 300] [-prices=false] [-keep]
```

## It cannot touch your data

- **A scratch folder of its own.** `-scratch` must be empty (or one an earlier measurement made, marked by
  `.stoxicity-measure`) and must not be, contain or lie inside the configured data folder. Every store,
  company list and quota record of the run goes there.
- **SQLite only.** A configured PostgreSQL server (`STOXICITY_DATABASE_URL`) is not used.
- **No document text.** Articles are built from the EDGAR index (form, company, day), as backfills are;
  filing documents are not downloaded. A sample of Form 4 XML is read for insider trades.
- **Removed afterwards.** The scratch database is deleted once the report is written, unless `-keep`.
- **Budgets.** `-max-requests` and `-max-mb` stop the run when reached; a stopped run is reported as
  NO-GO, with what it measured so far.
- **Free sources only.** The SEC (indexes, company records, XBRL frames, Form 4 XML) and the free plan of
  the price source, paced at five requests a minute. No order, signal or trade is produced.
- The window must have ended and be at most two months.

## What it does

1. Reads the SEC company list (tickers).
2. Reads the EDGAR daily index of each day of the window, for the forms of the filings source
   (8-K, 10-K, 10-Q, 4).
3. Reads the SEC's XBRL frames: reported figures of every filer.
4. Reads the SEC record of every company that filed: acceptance times and 8-K item numbers, and from
   the same record the filing day of those companies' figures (the frames do not carry it, and an
   earnings record only takes figures that were filed by its day).
5. Stores one backfilled article per filing and runs the real pipeline on it: scoring, events, episodes.
6. Reads a sample of Form 4 filings for insider trades (`-insiders`). Only open-market purchases and
   sales (codes P and S) of ordinary shares become insider records, so a small sample may give none.
7. Builds the event database from ten days before the window (a Form 4 follows its trade by up to two
   business days) and counts the records that became public inside it.
8. Fetches the daily bars from a week before the window to 40 days after it, and checks each event for
   a bar on its entry session and 20 sessions later, and the benchmark (SPY) on the same days.

## The report

`FILE.md` for people and `FILE.json` beside it, with the window, the code revision and the settings.

1. **What the free sources hold:** filings and companies per form; acceptance time known, day only, not
   in the SEC record, not read because of the budget; filings whose company has a ticker (the identity
   check); the most frequent 8-K items.
2. **Missing data against unrecognized data,** per PRD category:
   - *not recognized:* a form or 8-K item names the category, and the pipeline made no record of it.
     That is extraction work.
   - *not fetched:* only a document's text or XML tells, and the run did not read it. That is sourcing
     work, still free.
   - *no free source:* nothing free and authorized carries it (analyst revisions). That is a purchase
     decision, tier D.
   - *partly covered* and *covered* otherwise; earnings records without reported figures count as partly
     covered. The report also says how many 8-Ks had their item's event kind assigned by the pipeline.
3. **Prices and benchmark:** events with an entry bar and a bar 20 sessions later. The entry session is
   approximated as the first session after the event's day; a study's own rule is stricter.
4. **What the run cost:** requests, bytes and failures per host, time per step, the scratch database's
   size. Retries happen inside the HTTP client and count once.
5. **Projected six-month backfill:** the run scaled by days, with its assumptions.

The recommendation is NO-GO when the run stopped early, when more than a tenth of the index days stayed
unresolved, or when nothing was read; CONDITIONAL GO when any category is not covered, timing is
mostly unknown, the pipeline misses the kinds 8-K items name, or price coverage is short; GO otherwise.

A measurement says what the data and the pipeline can do. It says nothing about whether a strategy is
profitable.

## Running it

Pick a month that has ended at least 40 days ago, so the 20-session horizon has bars, and preferably an
earnings season:

```bash
stoxicity measure -from 2026-07-15 -to 2026-08-14 -scratch /tmp/stoxicity-measure
```

A month is about 30,000 to 40,000 filings and one SEC request per filing company: allow about an hour,
plus about 15 minutes for the bars on the free price plan.
