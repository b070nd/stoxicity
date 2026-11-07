# Can the program pick stocks? A review from the seat of a stock picker

Written 2026-11-07 after driving the interface (Home, Stocks, Companies, a
stock page) and the command line on the live database, then measuring the data
underneath. The question: is there enough here to decide which stocks to follow
and to build a portfolio, and what has to change so that there is.

**Short answer: not yet.** The program is a good *catalogue* (16,547 securities,
sorted, filtered, colour-coded) and a good *filing wire* (4,600 SEC filings in
30 days). It has nothing that tells you a stock is *worth* following: no price,
no fundamentals, filings attributed to the wrong companies, and no labels to
tune the scoring. All four are fixable with keyless public data, and the fix is
mostly one structural change (below).

This is not investment advice and it contains no list of stocks to buy. The
sample screen in section 4 is a mechanical test of the pipeline on public
filings, with its known faults listed.

## 1. What was done

- Drove the real interface on the real data through a scratch harness (keys in,
  screen out; removed afterwards). Nothing in your `stocks.yaml`, watchlist or
  databases was changed.
- Queried `data/base.db` directly to measure the news, the scores and the
  ticker attribution.
- Fetched the SEC's cross-company fundamentals and FINRA's short-volume file to
  see what a full-coverage source looks like (endpoints answered 200).

## 2. Findings, ranked by how much they block a decision

### F1. Filings are attached to the wrong companies (blocks everything)

**Fixed 2026-11-07.** Filings now carry their CIK (`Article.CIK`, stored in a
new `articles.cik` column, backfilled from the summary by migration 0007) and
get the tickers of that CIK and nothing else; `stoxicity relink` repairs stored
filings. Same audit on a copy of the live data after `relink`: 4,313 of 4,313
filings with a known CIK exactly right (was 632), none without a ticker (was
1,317), none with a wrong one (was 2,364). The word CIK and DE are also on the
ticker stoplist, and generic first words such as "Group" no longer match a
company by name alone. The text below is the finding as measured before the fix.

Audit of the 4,542 SEC filings whose text carries a CIK, checked against the CIK
to ticker table the program already holds:

| Result | Filings | Share |
|--------|--------:|------:|
| Tickers exactly right | 632 | 15% |
| No ticker at all | 1,317 | 31% |
| At least one wrong ticker | 2,364 | 55% |
| CIK not in the ticker list (funds, delisted) | 229 | 5% |

The wrong ones are not subtle. The literal word **"CIK"** is a ticker (UBS Asset
Management Income Fund) and was attached to **2,283** filings; `GPI` (112),
`DE` (41, a state abbreviation) and friends follow. On Home, "Sidus Space" is
tagged `SAAQ`, "Bowman Consulting" `GPI`, "Agility Robotics" `CCXI CCXIU CCXIW`.
The Stocks screen therefore ranks UBS's fund first with 25 "mentions" that are
all noise.

Cause: the entity matcher reads titles and summaries as free text. A filing is
not free text: its filer is named by CIK in the record itself, and the program
already maps CIK to tickers.

### F2. No price, no fundamentals, no valuation

**Fixed 2026-11-07.** Prices work live (see F7). Fundamentals: the SEC's XBRL
frames, 45 small requests in about ten seconds, give 120,000 facts for 7,570
filers (`stoxicity universe fundamentals`). Of the 5,952 common stocks, 4,754
(80%) have a market value and 3,881 (65%) a revenue; banks, insurers and REITs
use other revenue tags and are the main gap, and foreign issuers (IFRS) have
only a cover page, so they get no market value. The query language now has
`marketcap:`, `revenue:`, `income:`, `assets:`, `equity:`, `growth:`,
`margin:`, `roe:` and `equity_ratio:` and the matching `sort:` keys, and the
company list shows MCAP, REVENUE, GROWTH and MARGIN. The text below is the
finding as measured before.

- Prices: `no market data provider is configured`. The candle pipeline exists
  (Polygon, needs `STOXICITY_POLYGON_KEY`), so the DAY/CLOSE/CHG columns have
  stayed empty on live data. **Nothing has been tried against live Polygon.**
- Fundamentals: none. Yet the SEC publishes them for every filer, keyless.
  Verified today: `data.sec.gov/api/xbrl/frames/us-gaap/Revenues/USD/CY2024.json`
  is one 384 kB request that returns **2,511 companies' revenue**;
  `companyfacts/CIK##########.json` returns one company's whole history (3.9 MB
  for Apple).
- Without both there is no market value, no multiple, no yield: the first
  things a stock picker sorts by.

### F3. The scoring cannot separate stocks (and sits on a cliff)

**Partly fixed 2026-11-07.** Relevance is now `0.20 + 0.15 × (materiality − 1)`,
so materiality 3 gives 0.50, the default threshold, instead of 0.44 + 0.05 =
0.49; the cliff is gone and "new information on an expected path" counts. An
8-K takes its kind and materiality from its most material item (see
fetchers.md), not from the kind alone, and the other items add 0.02 each. A
10-Q is materiality 2 (its results were already in an 8-K item 2.02). Rescored
on the live data (5,081 articles): articles at or above 0.50 went from 48 to
1,447; 8-K results now take three values (0.55, 0.57, 0.59) instead of one, and
10-Q sit at 0.40. What is still missing is a ranking that looks at the
content: how good the results were, how big the company is, how the price
moved. That needs the signals layer (item 7) and the filing text. The text
below is the finding as measured before.

From `scores` (5,101 articles, each counted once, with its latest score):

| Fact | Value |
|------|-------|
| Relevance >= 0.50 (counted as "news") | **48** articles |
| Relevance 0.40 to 0.49 | **3,228** articles |
| 10-Q: 1,807, all `results`, all relevance 0.49, all materiality 3 | the cliff |
| 8-K `results`: 1,118, all 0.49 / materiality 3 | same |
| Mean absolute tone | 0.00 to 0.02 (filings have no tone) |
| Materiality | one of 1 to 4, decided by form alone |

Relevance 0.49 against a threshold of 0.50 means every earnings filing falls
just under the line while 48 odd items pass. The value is a function of the
form type, not of the filing's content, so it cannot rank one 8-K against
another. The 8-K *item codes* (2.02 results, 4.02 non-reliance, 3.01 delisting,
5.02 officer change, 1.03 bankruptcy...) are stored (1,901 articles have them)
but the page shows only the title.

### F4. The most useful public signal is thrown away (fixed for the data, not for the score)

871 Form 4 filings (insider transactions) were scored `other`, materiality 1,
and never parsed. Open-market insider purchases are one of the few free signals
with published evidence behind them, and the buy/sell, price and size are in the
filing's XML.

**Now** every Form 4 in the database is read once (one 6 KB request each, 874
filings in 79 seconds, none unreadable) and its trades in ordinary shares are
stored: 1,821 trades, of which 133 are open-market purchases (code P) and 984
sales (S); the rest are awards, tax withholding, exercises and gifts. 633 of
the sales say they were made under a Rule 10b5-1 plan, which is why a sale says
little and a purchase a lot. In the last 90 days 54 listed common stocks have an
insider purchase, 30 of them of $100,000 or more and 13 by two or more different
people (`type:stock buyers:>=2 sort:-buys`). The stock page lists the last ten
trades; the list has BUYS and SELLS columns.

What the data cannot say yet: whether a "buyer" is an officer buying with their
own money or a fund that holds a board seat (the biggest 90-day purchases in
this sample are two Baker Bros. funds, as directors), and a purchase does not
raise the score of its Form 4 *article*, which is still materiality 1: the score
is set when the article is ingested, before the filing is read. Fixing that
needs the signals layer (section 3), not another special case.

### F5. There is nothing to tune against (the tools exist, the labels do not)

`label list` is empty: **zero** keep/drop labels, so `eval` has nothing to
measure and the lens version has never been checked. The `+`/`-` keys existed
but nothing prompted their use or showed which items would teach the lens most.

**Now** the Label screen (`a`) and `stoxicity label queue` offer the articles
worth judging first (nearest the threshold, kind by kind, one per story, plus a
random fourth that keeps the precision honest), show progress towards 300 labels
and the lens's precision and recall in the status line, and `stoxicity eval`
prints them per kind of event as well. What is still missing is the labels
themselves: they are the user's judgement and cannot be made by the program.
About 1,447 articles are at or above the threshold and 5,000 in all, so 300
labels is a small slice; the queue is built to spend them where they teach most.

**Weak labels from item codes were not built, on purpose.** An 8-K's relevance
is *already* computed from its item codes (section 2, F3), so labelling by item
and then evaluating the lens against those labels would measure the lens
against itself and always look good. They would help only as training data for
a model such as laya, and nothing in the repository trains one yet. If that
changes, store them as a separate source and keep them out of `eval`.

### F6. The news is regulators, not companies

All 19 enabled sources are central banks, the SEC and the Fed. Company news
feeds: 0% (the website-feed discovery has found none), and the sources bar says
"0 ok 19 stale" because `run` is not running (last poll 00:01). For a picker
that means filings are the only company-level event stream, which is another
reason F1 and F3 matter.

### F7. Smaller things found while using it

- **Type column (my bug, fixed here).** SPAC units, warrants and rights spelled
  AACIU/AACIW/AACIR were shown as "stock", and so were warrants such as VFSWW
  whose SEC name is the company's. The type is now read first from the Nasdaq
  directory's own security name ("... - Warrant", "... - Units", "... Senior
  Notes due 2030"), then from the sibling ticker and the company name. Of the
  live catalogue 5,952 are plain common stock, 5,779 ETFs, 845 preferred, 828
  ADRs, 556 warrants, 282 units, 176 notes and 125 rights: any screen must
  start with `type:stock`.
- **Prices work live (2026-11-07).** With a Massive (formerly Polygon) key the
  day candles load for 87% of the stocks. Two faults found on the way: Polygon's
  results carry both `T` (ticker) and `t` (time), which Go's case-insensitive
  JSON decoding mixed up (fixed by reading the exact keys), and some series are
  mixed (BCPC showed +710% in a day). A move beyond five times or below a fifth
  of the day before is now shown as no change, so it cannot top a `sort:change`.
- 74% of companies still have no SEC record loaded (26% do), so DOMAIN is blank
  for them; `universe enrich -all` (about 20 minutes) fixes it and should be a
  one-time bootstrap in `run`.
- The Stocks screen sorts by mentions; with F1 fixed it still lacks a way to
  say "show me what changed since I last looked".

## 3. Structure: the brave change

Today everything is an **article**: prices, fundamentals, insider trades would
all have to pretend to be text with a title. Scoring, watches and the screens
then work on text. That is why the program cannot answer a stock question.

Proposed shape (three new layers, existing code kept where it fits):

```
sources ──► ingest ──► identity ──► facts ──► signals ──► screens ──► portfolio
 (RSS,       articles    CIK-first    price,     per stock   declarative  lists with a
  EDGAR,     + records   linking      fundamentals, per day   YAML rules   thesis, role,
  XBRL,                  (F1)         insiders,   (typed,    + ranked     review date
  FINRA)                              shorts      explained) output
```

1. **Identity first.** Every record gets `Entities []{CIK, Ticker, Role
   (filer | subject | mention), Method (cik | exact | fuzzy), Confidence}`. SEC
   sources link by CIK only (100% by construction). Free-text linking is
   allowed only for real news, and only for tickers written as `$XYZ` or
   `NASDAQ: XYZ`, never from bare uppercase words. A stoplist (`CIK`, `DE`,
   `US`, `CEO`...) is the safety net, not the design.
2. **Facts.** New tables next to `candle`: `fact(cik, concept, period_end,
   fiscal_year, fp, value, unit, filed, form)` filled from XBRL frames/company
   facts, plus `insider(cik, filed, owner, role, code, shares, price)` from Form
   4. Views for "latest fiscal year", "trailing twelve months" and growth that
   align on fiscal-year end (see section 4: calendar frames miss Apple,
   Nvidia and General Mills' year).
3. **Signals.** One table `signal(ticker, day, kind, value, source, ref,
   reason)`: `filing.8k.2.02`, `insider.buy`, `price.move`, `short.ratio`,
   `news.count`. The lens, the watches and the screens read signals, not
   articles. An article becomes one signal among others, which also fixes F3:
   materiality comes from the signal kind (an item 4.02 non-reliance is not a
   10-Q), and relevance stops being a threshold on text.
4. **Screens and portfolio.** A screen is a file (`screens/quality.yaml`:
   filters, ranking, the columns to show) run by `stoxicity screen NAME` and
   shown in the interface as a page; every row says *why* it is there. A
   portfolio is a list plus, per stock, a **role** (core, satellite, watch), a
   **thesis** note, a **review date** and **exit rules** expressed as watches
   ("alert if insider sell > X, or an item 4.02, or a close below Y"). Today a
   list is only membership.

## 4. Prototype: the pipeline end to end, with its faults

A mechanical screen over SEC frames (calendar 2024, revenue at least $5B,
growth above 5%, net margin above 10%, return on equity above 15%, equity over
assets at least 15%) joined to the program's own NYSE/Nasdaq common-stock list
gave 38 of 384 candidates. It runs in under a minute from seven requests.
The result is **not** a recommendation, and it exposes the design lessons:

- **Calendar-year frames and fiscal-year companies.** The prototype lost Apple
  (September) and Nvidia (January) and showed General Mills growing 856%
  because it trusted the frame's name. Reading the frames properly shows each
  datum carries its own `start` and `end` (Apple's CY2024 datum ends 2024-09-28,
  General Mills' ends 2025-05-25), so the fact layer keys on those dates: a year
  is a span of 350 to 380 days, growth compares year-ends 340 to 390 days apart,
  and the balance sheet is the one within 45 days of the year-end. Nvidia was
  absent because it reports under another revenue concept, hence the fallbacks.
- **Concept names differ** (`Revenues`, `RevenueFromContractWith...`,
  `SalesRevenueNet`; the last 404s for 2024), so a concept map with fallbacks is
  needed, and a banks/insurers rule (equity over assets is meaningless there).
- **Size is missing** without price. Market value = shares outstanding (a
  `dei` fact, also in frames) times the close; until prices exist the screen can
  only use revenue.
- Every row must carry its numbers and period, so a human can check it.

## 5. What to build, in order

| # | Work | Why first | Done when |
|---|------|-----------|-----------|
| 1 | **Done.** CIK-first entity linking for SEC sources; stoplist; `relink` | F1 blocks all else | the audit script gives 100% exact on filings with a known CIK; "CIK" is no ticker |
| 2 | **Done.** Key and run Polygon candles; show DAY/CLOSE/CHG live; coverage line "price N%" | F2, needs one key | the company list shows a candle for at least 90% of `type:stock` on NYSE/Nasdaq |
| 3 | **Done.** Fundamentals store from XBRL frames (fiscal-aligned) + market value + shares | F2 | `sort:marketcap`, `revenue:>5b`, `margin:>10%` work in the query language |
| 4 | **Done (items only).** Materiality from 8-K item codes; show decoded items on the page; fix the 0.49/0.50 cliff | F3 | 10-Q/8-K 2.02 no longer sit at one value; a ranking exists inside "results" |
| 5 | **Done.** Form 4 parser, insider figures (`buys:`, `sells:`, `buyers:`, `sellers:`) and BUYS/SELLS columns | F4 | a stock page lists the last insider trades with size and price |
| 6 | **Built (needs your 300 labels).** Label screen and `label queue` with the most informative unlabelled items, `eval` per kind and in the status line. Weak labels from item codes: not built (circular, see F5) | F5 | 300 labels, a printed precision/recall per kind |
| 7 | Signals table and `screen` command, portfolio roles and theses | structure | one screen and one portfolio defined in files and visible in the interface |
| 8 | More event sources (candidates to verify before relying on them: FINRA short volume `cdn.finra.org/equity/regsho/daily/CNMSshvolYYYYMMDD.txt` answered 200 today; Nasdaq trading halts, GDELT, company IR feeds are **unverified**) | F6 | each source has a sample fixture and a measured hit rate |

Items 1 to 3 turn the program from a catalogue into something a screen can run
on; 4 to 6 are where the fine-tuning data comes from; 7 is the structural step.

## 6. Reproducing the numbers

- Attribution audit: for each `articles` row of `sec-edgar`, take the CIK from
  `summary` (`CIK ##########`), map it with `ref_ticker`, compare with
  `articles.tickers`.
- Score tables: `scores` joined to `articles` on `article_id`, grouped by
  `articles.form` and `scores.kind`.
- Fundamentals: the five frames used were `Revenues`,
  `RevenueFromContractWithCustomerExcludingAssessedTax`, `NetIncomeLoss`,
  `StockholdersEquity` and `Assets`, for CY2023 and CY2024 (instant concepts at
  `CY2024Q4I`), all from `data.sec.gov`.


## Corrections after the red-team review (PROTOCOL.md)

The review in `PROTOCOL.md` found cases where a screen showed a confident wrong
number. What changed, and what is still open (the status of every finding is in
`PROTOCOL.md`, "Remediation status"):

- **Day change** is a session change: it is shown only when at most one weekday
  (a holiday) lies between the two closes, and not when the ratio of the closes
  is the ratio of a split (2:1, 3:1, 1:10 ... within 1.5%). This is a heuristic
  on prices adjusted on the day they were fetched, not a corporate-action
  reconciliation, so a real move of exactly that size is also hidden.
- **Market capitalisation** uses only the share count on the cover page of the
  filing (`dei:EntityCommonStockSharesOutstanding`), never the weighted average,
  and is left out for a company with more than one class of common stock
  listed under one CIK (GOOG and GOOGL show none), because one class's count is
  not the company's. **Revenue and income** are paired only when they are of the
  same fiscal year; otherwise the older one is not shown next to the newer.
- **Insiders**: purchases (P) and sales (S) are private as well as open market.
  Only common stock counts (a preferred or note purchase does not), a 4/A
  replaces the earlier forms of the same owner and period, a trade without a price
  is counted apart, and the summary on the list and on the stock page come from
  the same trades. Migration `0010` discards the stored trades so the filings are
  read again with these fields (`stoxicity universe insiders`, about 80 seconds
  for 874 filings).
- **The stock page's "Latest news"** ignores an older answer that arrives after a
  newer one.
