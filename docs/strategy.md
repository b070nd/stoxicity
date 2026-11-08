# Valuation scenarios and the core strategy

Story US-010 of [the production plan](production-plan-mode.md). `stoxicity strategy` turns a
[scorecard](scorecards.md), the declared payouts, a price and **your written valuation assumptions** into a
proposal with its checklist. **It proposes; it never orders.** The starter strategies are *unvalidated initial
research settings*: they have not been tested against outcomes, nothing here is evidence of an edge, and no number
is a probability or a forecast.

## What a proposal says

Four fields are kept apart: `action`, `data_state`, `strategy_state` and `execution_eligibility`
(always `research_only` here; broker preflight comes later).

| Action | Meaning |
|---|---|
| `BUY` / `ADD` | eligible, a valuation exists, a current price is inside the entry band, and the configuration has been **reviewed**. An owned security reads ADD |
| `WATCH` | eligible, but there is no valuation, the price is outside the band, or the configuration is unreviewed |
| `WAIT` | eligible and valued, but there is no current price (none, older than the limit, or in another currency) |
| `ABSTAIN` | the checklist is not met, or a required input is missing; the reasons say which |

`HOLD`, `REDUCE` and `SELL` are decided for owned positions by the review rules (US-013), never inferred from missing data.
News volume is recorded and ignored: the volume of news cannot produce a proposal.

## The checklist (`quality-income-core-v1`)

100 points in five groups, every test persisted with its result. A **required** test that is unknown blocks (`ABSTAIN`,
`data_state: insufficient`); one that fails makes the company ineligible. An optional test that is unknown earns nothing,
and **scores are never scaled up** for what is missing; coverage (tests with a known result / applicable tests) is printed.
Proposal threshold: at least `min_score` (70) and every required test. A company and a fund are never compared by number.

| Group | Tests |
|---|---|
| business understanding (20) | a sourced thesis note (required, 10); moat note (5); management note (5) |
| financial quality (25) | at least 3 comparable completed years (required); positive latest net income and free cash flow, free cash flow positive in 2 of the last 3 (required, 5 each); net margin and ROE above floors (5 each) |
| resilience (20) | net debt / EBITDA at most 3 and operating income / interest at least 4 (required, 10 each); a **sourced zero total debt** is an explicit exception, not a division by zero |
| valuation (20) | scenarios computed from documented assumptions (10); price at or below the entry ceiling (10) |
| sustainable income (15) | declared ordinary distribution (5); at least 4 declared ordinary payouts in five years and none cancelled or contested in the last year (5); dividends paid at most 80% of free cash flow and 70% of net income (5). A company with no ordinary distribution gets *not applicable*, can qualify for the value strategy and gets no income preference |
| evidence | no unresolved going-concern, restatement or material-weakness counter-note, no identity conflict (required) |

Income preference is a **tie-breaker** among independently qualifying proposals (after portfolio fit and cost, in the allocator), never
points for a high headline yield or a near ex-date. Special and return-of-capital payouts are not ordinary income.

## Valuation

```bash
stoxicity strategy assumptions example      # JSON with placeholders you must replace
stoxicity strategy assumptions set -security S -file assumptions.json
```

An owner-earnings discounted cash flow: the latest free cash flow per share (operating cash flow less capex, divided by the stored share
count, times an **explicit** normalization factor) grown for N years at the scenario growth, discounted at its rate, plus a terminal value.
Growth, discount rate and terminal growth are yours for each of bear, base and bull; there are no defaults, and the source and reference
of the assumptions are required. The arithmetic is exact (rational) and rounded once, half-even, to four digits. Every proposal shows a 3×3
sensitivity of the base value to the discount rate (±1 point) and terminal growth (±0.5 point). The **entry ceiling** is the
`entry_scenario` value (base or bear) less the margin of safety (20% in the starter configuration). A price in another currency than the
valuation is not converted: the entry test is unknown. Without assumptions there is research output and no invented price.

## Configuration and review

```bash
stoxicity strategy config example | set -file F | review | show
```

`set` stores a configuration **unreviewed**; `review` is a separate act that records that you read it. Until then no proposal is stronger than
WATCH. Every threshold is in the file and in the proposal's hash, with the scorecard hash (which carries the keyword, extractor and rule versions).

## Cash and a fund as alternatives

Every proposal lists cash and a diversified fund as alternatives, without claiming to beat them; a cash yield you supply is shown beside the
latest free-cash-flow yield (a historical figure, not a return). `strategy fund` runs `diversified-core-fund-v1`: **checks, not a score**, for
documented ongoing cost, tracking difference, domicile, replication structure, distribution policy, a dated look-through with enough holdings,
and currency exposure. An undocumented fact is *unknown*, never a pass; a dividend-tilted fund is compared with a broad core alternative.

## Not done here

- Held-position hold/reduce/sell (US-013), portfolio allocation (US-011), the immutable plan snapshot (US-012) and the event strategy (US-020).
- Banks, insurers and REITs have no strategy template yet (their scorecards are limited to the ratios that mean something).
- Price comparison across currencies (no rate is applied) and valuation from stored FX observations.
