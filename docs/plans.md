# Plan snapshots and their lifecycle

Story US-012 of [the production plan](production-plan-mode.md). A plan is a stored answer to one question:
*what should be done about this security in this book under this strategy?* Every time it is generated,
the answer is kept as a **revision** that is never edited. A plan is a record of a view and of your decision
about it. **It places nothing and is not an authorization**: accepting a plan is your decision on record,
and an order still needs its own preflight and confirmation (later stories).

```bash
stoxicity plan generate BOOK -candidates cands.csv -fees fees.json [-as-of DAY] [-rates USD=0.92] [-contribution A] [-valid-days 3]
stoxicity plan list [-all] | show PLAN[.rN] [-json] | diff PLAN [-from N -to M] | check PLAN[.rN]
stoxicity plan propose|accept|reject PLAN[.rN] -by NAME [-note TEXT]
stoxicity plan expire
```

`generate` reads the same candidate CSV and fee schedule as [allocation](allocation.md) and writes one
plan per security: allocated purchases are BUY or ADD with a quantity; a stock the strategy likes but the
allocator cannot afford is WAIT with the reason; the rest keep the strategy's own WATCH, WAIT or ABSTAIN.

## What a revision holds

- **The card** (section 5.6 of the plan): identity, action, data state, strategy state, execution eligibility,
  reasons and unknowns, thesis and counter-evidence, every scored test, bear/base/bull values and the entry
  ceiling, quantity with the price it was sized at, notional, fee, cash before and after, the issuer
  exposure before and after, separate cost lines (commission and FX, spread, tax: an unavailable one says
  so), horizon, next review, invalidation, the reduce and sell rules, alternatives and what would change it.
  Execution eligibility is `research_only` until the later execution stories exist.
- **The manifest**: the exact inputs it was computed from: policy epoch and hash, strategy configuration,
  scorecard hash, valuation assumptions, price, the count of dividend observations, the fee schedule, the
  rates and the fingerprint of the book. Its hash and the card's semantic hash are stored; `show` re-checks
  them and says INTEGRITY if a stored revision no longer matches.
- **Expiry**: every plan has one (default 3 days after its as-of day). A plan past it is expired when read,
  and `plan expire` records that. An expired plan is never revived.

## Reproducible and as-of

The semantic hash covers the card and the manifest and **not** the time of generation. Generating again
from the same frozen inputs gives the same hash and stores nothing new (`unchanged`). With `-as-of`, the
policy in force that day, the book as it was *recorded* by then, and only evidence known by then are used; a
day before any policy existed is refused. Without `-as-of` today is used.

## Revisions and the lifecycle

```
draft -> proposed -> accepted | rejected | expired | superseded
```

- A refresh appends revision N+1 with the **diff** to N, each change marked material or not. Material means it
  alters what you would do or why: action, eligibility, data or strategy state, quantity, price bounds,
  ceiling, any test result, the unknowns, reasons or invalidation. A reworded note or a later review date is not.
- A draft or proposed predecessor is superseded by the new revision. An **accepted** revision is not touched: it
  stays in force until you accept the new one, so a refresh cannot silently rewrite what you accepted.
- Moves need `-by` (who decides); a rejection needs `-note`. Only BUY, ADD, REDUCE and SELL can be accepted; a
  WATCH, WAIT, ABSTAIN or HOLD records a view and has nothing to accept.
- **Downstream invalidation**: a revision is *stale* when an input it names changed since: the policy (a new
  activation), the strategy configuration, the scorecard (or anything written to it after the plan was
  generated), the assumptions, the dividend observations, or the book. A stale revision stays readable and is
  never rewritten, but it cannot be accepted: regenerate it. The price, fee schedule and rates came from files
  you supplied, so only the expiry covers them.
- Card rules hold whatever produced the card: an unheld instrument cannot receive ADD, a sale cannot exceed the
  free (unreserved) holding, a plan without an expiry is refused.

## Where it is stored

In the tenant's record store as two append-only kinds: `plan.revision` (one record per revision) and
`plan.event` (one per lifecycle move, numbered). Nothing is updated in place.

## Not done here

- Exits and reviews of what you hold (HOLD, REDUCE, SELL) are US-013; the revision machinery already enforces the
  quantity bound for them.
- Turning an accepted plan into an order intent is US-014.
- Two processes moving the same revision at the same moment are not serialized; the single-user command line
  is the supported use.
- Evidence excerpts and links are the scorecard's sourced notes; there is no separate evidence-relationship graph.
