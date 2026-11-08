# Sourced research scorecards

Story US-009 of [the production plan](production-plan-mode.md). A scorecard is a multi-year, readable
assessment of a business built only from figures you supplied, each with its source and reference.
**Nothing is fetched and nothing is estimated**: a figure that is not there stays visibly missing, and a
ratio whose inputs do not fit says why it is blank. A scorecard is research input, not advice, and it is
not a recommendation to buy.

## Figures

```bash
stoxicity scorecard example                      # a CSV in the format, with invented numbers
stoxicity scorecard import -file figures.csv     # all-or-nothing; every problem is listed with its line number
```

Columns: `security, metric, end, value, source, ref` and optionally `start, currency, filed, note`.

- **Exact decimals in whole currency units**, never thousands. Capex, interest expense and dividends paid are
  entered as positive amounts spent.
- **Source and reference are required.** A number with no origin (for example "Annual report 2025 p.41", an
  accession number, an ESEF package) is refused. Manual entries and European filings are therefore as traceable as
  SEC data: the register of securities must contain the security, but where the figure came from is yours to state.
- **Periods are annual or quarterly only.** A transition or cumulative period is refused so it cannot be taken for a
  year. Ratios use annual figures; a year that has only a quarter shows `period mismatch`.
- **`filed`** is the day the figure became public. Reading the card `-as-of` a day uses only figures filed by then, and
  a fiscal year none of whose figures had been filed yet does not exist on that day. A figure with no filing date cannot
  be shown as of a day (`not yet filed`); a restatement filed later does not leak into an earlier reading.
- Two sources that disagree on the same filing day are a `conflicting` cell, not an average.

## Sector templates

```bash
stoxicity scorecard template -list
stoxicity scorecard template -security sec_x -template ordinary|utility|bank|insurer|reit|fund|unknown
```

Until a template is chosen the template is `unknown` and **no ratio is computed**. A bank, insurer, REIT or fund is not an
ordinary company: their revenue, debt and cash flow mean something else, and the measures that matter (regulatory capital,
solvency, funds from operations, look-through holdings) are not in this data model, so ordinary-company ratios are
refused with that reason instead of approximated. Banks and insurers get ROE, equity/assets and payout; a REIT gets revenue
growth, interest cover and equity/assets; a fund none.

## Ratios and when they are blank

Revenue growth, operating margin, net margin, return on equity, debt/equity, interest cover, free-cash-flow margin and
conversion, payout ratio, equity/assets. Each cell is a value or a status with a reason: `missing`, `unsupported`,
`negative denominator` (for example negative equity), `zero denominator`, `period mismatch`, `currency mismatch` (no rate is
applied, ever), `no prior year`, `conflicting`, `not yet filed`. The cell also records the content hashes of the figures it was computed from.

## Qualitative notes

```bash
stoxicity scorecard annotate -security S -topic risk -stance counter -text "..." -source analyst -ref "memo 3"
```

A note has a topic, a stance (supports, counter, neutral), a source and a reference; an opinion with no origin is not
kept. Notes never change a number. A plain, case-insensitive **keyword table** tags them with risk themes (going concern,
restatement, litigation, refinancing, ...): a flag says a phrase is present, not that the risk is real. The strategy later
reads `counter` notes as counter-evidence.

## Versions and the content hash

`scorecard versions` prints the extractor, rules and keyword-table versions. The rules and keyword versions carry a digest of
their definitions, and all three are part of every card's content hash and are written into plan manifests, so a change to
the keyword table, the extractor or a ratio definition is visible as a changed hash rather than a silent change of meaning.

## Not done here

- Fetching filings (SEC frames exist in the news pipeline; they are not yet mapped into this model) or reading ESEF/XBRL packages directly.
- Quarterly/trailing-twelve-month ratios, per-share figures, bank and REIT specific measures.
- Any conclusion: valuation and proposals come from US-010.
