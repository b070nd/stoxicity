# Holding, reducing and selling: the review of what you own

Story US-013 of [the production plan](production-plan-mode.md). `review run` looks at every position in
a book and says HOLD, REDUCE, SELL or ABSTAIN, with the rule that decided and the conditions that argue
against it. It proposes quantities from **free** shares only (those not reserved by an open sell order), and
it places nothing. **None of the thresholds is validated**, and the rule set starts unreviewed.

```bash
stoxicity review run BOOK [-prices FILE] [-as-of DAY] [-rates USD=0.92] [-save] [-valid-days N] [-json]
stoxicity review thesis set -security S -status intact|under_review|broken -note T -source S -ref R -by NAME [-depends-on-dividend]
stoxicity review instruct -security S -action reduce|sell (-qty Q | -all) -why withdrawal|liquidity|instruction -by NAME | -clear -by NAME
stoxicity review done -security S -outcome held|reduced|sold|noted -note T -by NAME
stoxicity review config example | set -file F | review | show
```

`-prices` is the candidates CSV of [allocation](allocation.md) (`allocate example`): a price you supply, or
the stored close for `mic` and `ticker`. A holding with no usable price is not valued, and the review says so.

## The reasons, in order

The first reason that dictates a sale decides. Every finding is still printed, so a sale always shows its
rule and the conditions beside it ("against:").

| # | Reason | What it does |
|---|---|---|
| 1 | **Your instruction** (`review instruct`): a withdrawal or liquidity need | REDUCE or SELL the quantity you gave, at most the free shares; says when it was capped |
| 2 | **A thesis you recorded as broken** | SELL the free shares. A thesis `under_review`, a thesis never recorded, and a **dividend cut or cancellation** each require a review and propose **no sale**. With `-depends-on-dividend` a cut is called inconsistent with the thesis, still a review, not a sale |
| 3 | **Concentration** over a policy limit (issuer, fund), or the **approaching goal** | REDUCE by the *smallest* quantity that cures the breach, bounded by free shares; the part that cannot be cured now is stated. Inside the goal window it proposes the configured de-risk fraction, or only a review when that is zero. Where the proceeds go (cash, other non-equity choices) is yours to decide |
| 4 | **Valuation** above the configured scenario (default: the bull case) | REDUCE the configured fraction. It does not weigh alternatives net of costs and taxes (none are supplied) and says so |
| 5 | **Event position** past its holding limit or invalidated | SELL. Sessions are counted as weekdays; holidays are not subtracted |
| 6 | **Scheduled rebalance** | not evaluated: no target weights exist. Every review lists it under "not evaluated" |

Dividend cuts come from the stored distribution history ([dividends.md](dividends.md)); without a split
history a cut may be a share split, and the review says so.

## What it will not do

- **Missing data never becomes HOLD and never becomes a sale.** With no price, no rate, or insufficient
  data for the holding, the review is `ABSTAIN (blocked)` and lists what is missing. Your recorded decisions
  (1 and 2) still apply, and a sale then shows its proceeds as **unavailable**, never invented. With a current
  price the proceeds are an estimate before costs and taxes.
- **A review date is not a sale date.** The next review is the last recorded review (`review done`), or the
  purchase day, plus the interval (91 days). A late review reads `HOLD (review overdue by N days)`, a review
  that a finding requires reads `HOLD (review required)`; neither is a fresh HOLD.
- **No sale when nothing is free.** If a rule calls for a sale but every share is reserved, the result is
  `HOLD (blocked)` with the reason. Pending buy orders are not modelled here (US-014).
- **Unreviewed thresholds propose no sale.** Until `review config review`, the goal window, valuation and event
  limit only require a review. Your own instruction, your recorded broken thesis and your policy limits do not wait.
- **Equity** is the base-currency cash, cash in other currencies at the rates you supply, and the holdings that
  could be valued. A missing rate or price is stated as a caveat on every review; exposure shares are then overstated.

## Saved reviews

`-save` stores each review as a [plan](plans.md) revision (strategy `holdings-review-v1`), with the findings as
reasons, what was not evaluated as unknowns, and a quantity for a REDUCE or SELL. The revision names the
recorded thesis, instruction and review log it read, so recording a new one makes the saved review **stale**:
it stays readable, and cannot be accepted until it is regenerated.

## Not done here

- Rule 6 (rebalance) needs target weights; none are defined.
- Taxes, costs and the comparison of alternatives net of both (the spec's rule 4 wording): only a flagged absence.
- Sector exposure (no sector in the instrument register) and fund look-through.
- Event positions: the event sleeve (US-020) does not exist yet; the rule is implemented and tested on its own.
- Open buy orders and their effect on concentration (US-014).
