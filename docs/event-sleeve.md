# The event sleeve: an experimental, paper-only template

`stoxicity event` is a **separate, time-bounded sleeve** next to the core. It has one template,
`earnings-followthrough-paper-v1`, which is explicitly **experimental**. It proposes **paper candidates**
and exits only. It is never offered for a real account (`real_allowed` is always false in the output),
whatever the tier, and a position it opens is never moved into the core: that would need a new thesis and
your recorded decision.

The numbers in the template are arbitrary, testable starting settings. They are **not a claimed edge** and
nothing here says earnings events pay.

## Statuses

| Status | Meaning |
| --- | --- |
| `PAPER` | Every required test passed. A quantity, limit, invalidation and horizon are proposed, for a paper book. |
| `RESEARCH` | A required fact is **unknown**: unverified source, unresolved issuer or counterparty, unknown timing, too little comparable history, price series not split-consistent, no fee schedule, unreviewed configuration. No quantity is proposed. |
| `WAIT` | A test was evaluated and **failed**, or it is too early: scheduled or rumoured event, correction, repeated story, no follow-through, illiquid, risk too wide, trade does not pay for its costs, unaffordable. |

There is no real status. Material news alone never produces a trade, and a count of favourable words is
not an input.

## The tests (all are shown, with their result)

1. **Event kind and status**: a *completed* earnings event; scheduled or rumoured events wait.
2. **Source verified**, with the source and a reference you can check; `verified` stays false until you have.
3. **Issuer attribution**: the event's issuer must be the issuer of the security; a counterparty that is
   unresolved is research only.
4. **Timing known**: pre-market, or after the close. After the close, the reaction session is the next one.
5. **Origin unique**: the same underlying story repeated by other outlets is not a second event.
6. **Not corrected**: a corrected or contradicted event waits.
7. **Comparable results**: at least 2 comparable years and no unresolved contradiction, referenced.
8. **Price series**: finished daily bars, split-consistent, with the price source named.
9. **Follow-through** (an independent price filter): the event session closes above the highest close of the
   previous 20 completed sessions, with volume at least 1.5 times their mean.
10. **Liquidity**: average daily traded value at least 1,000,000 and the order at most 2% of it.
11. **Gap**: a gap beyond 5% is flagged, and the invalidation must still exist.
12. **Risk width**: entry to invalidation at most 15% of the entry.
13. **Cost feasible**: the round-trip fees against the notional, at most 2%, using a sourced fee schedule.
14. **Affordable size**: the quantity is the smallest of the planned-loss budget, the sleeve cap, the free
    cash and the liquidity share. Zero means wait.

`--bars` are the finished daily bars the paper engine reads (CSV: security, day, open, high, low, close,
volume, currency). Only bars before the evaluation day count, so an unfinished session is never used.

## What a candidate states

Catalyst (the event), entry not before the session after the event, a limit price (the event close plus 1%,
rounded up), the **invalidation** (the event-session low, used only when it is below the close), the horizon
(a 10-session review or time exit, configurable from 5 to 20), an optional 2R profit-review level (a rule, not
an expected return), the **planned loss**, the round-trip fees, and a hash of every input (the manifest). The
same inputs give the same hash.

Planned loss per share is entry minus invalidation plus a cost allowance. The quantity is at most equity times
the risk fraction (0.25%) divided by it. **Planned loss is not a guaranteed maximum**: a gap can pass the
invalidation, and an exit then does not get the invalidation price. When a policy is active, its
`max_event_sleeve` and `max_event_trade_risk` lower the sleeve and risk used here. The sleeve is 5% of equity
by default.

## Configuration

`event config example|set -file|review -by|show`. A new configuration starts **unreviewed**, and an
unreviewed configuration can only produce `RESEARCH`. Reviewing it does not make the template fit for a real
account.

## Exits

```
stoxicity event review -proposal EVENT -entry-day D -entry-price P -bars bars.csv [-as-of DAY]
```

Reports one of `HOLD`, `TIME_EXIT`, `INVALIDATED`, `PROFIT_REVIEW`. The time exit counts weekdays since the
entry (the entry day and the review day are not counted); the invalidation is checked against the bars, and a
session that opened through it is reported as a gap. It is a **distinct exit proposal in the event sleeve**:
nothing is sold, and a profit review is a review point, not an instruction.

## Limits you should know

* Equity, cash and bars must be in the **same currency**; nothing is converted.
* Sleeve lots are not tagged in the ledger, so a review takes the holding you state or a saved proposal.
* The time exit counts weekdays: exchange holidays are not known.
* Every setting is illustrative and unreviewed until you review it, and none is validated: that needs the
  walk-forward evaluation (US-023), which has not been done.
